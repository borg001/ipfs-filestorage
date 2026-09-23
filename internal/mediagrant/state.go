package mediagrant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/redis/go-redis/v9"
)

// State contains no access decisions. Versions only invalidate capabilities;
// active mutation guards prevent issuing grants from a partially changed file.
// This Redis database must use noeviction. Losing the epoch invalidates all
// outstanding grants; a missing dependency is never treated as permission.
type State struct {
	client *redis.Client
	prefix string
}
type Snapshot struct {
	Epoch    string
	Versions map[string]string
	Blocked  map[string]string
}

func NewState(redisURL, namespace string) (*State, error) {
	if namespace == "" || strings.ContainsAny(namespace, "{} \t\r\n") {
		return nil, errors.New("media grant state namespace is required")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, errors.New("invalid media grant Redis URL")
	}
	options.ContextTimeoutEnabled = true
	options.DialTimeout = time.Second
	options.ReadTimeout = time.Second
	options.WriteTimeout = time.Second
	options.MaxRetries = 0
	return &State{client: redis.NewClient(options), prefix: "media-grants:{" + namespace + "}:"}, nil
}

func randomVersion() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *State) Close() error { return s.client.Close() }

// Initialize is called by the issuer at startup, never by a downloader.
func (s *State) Initialize(ctx context.Context) error {
	policy, err := s.client.ConfigGet(ctx, "maxmemory-policy").Result()
	if err != nil {
		return fmt.Errorf("check media grant Redis eviction policy: %w", err)
	}
	if policy["maxmemory-policy"] != "noeviction" {
		return errors.New("media grant Redis requires maxmemory-policy noeviction")
	}
	value, err := randomVersion()
	if err != nil {
		return err
	}
	return s.client.SetNX(ctx, s.prefix+"epoch", value, 0).Err()
}

// Snapshot atomically reads the epoch, versions, and write guards. A blocked
// resource must not prevent a batch from issuing grants for unrelated files.
var snapshotScript = redis.NewScript(`
local epoch = redis.call('GET', KEYS[1])
if not epoch then return redis.error_reply('MEDIA_EPOCH_MISSING') end
local result = {epoch}
for i = 2, #KEYS, 2 do
  result[#result+1] = redis.call('GET', KEYS[i]) or '0'
  result[#result+1] = redis.call('GET', KEYS[i+1]) or ''
end
return result`)

func (s *State) Snapshot(ctx context.Context, resources []string) (Snapshot, error) {
	keys := []string{s.prefix + "epoch"}
	for _, resource := range resources {
		keys = append(keys, s.prefix+resource+":version", s.prefix+resource+":guard")
	}
	values, err := snapshotScript.Run(ctx, s.client, keys).StringSlice()
	if err != nil {
		return Snapshot{}, err
	}
	if len(values) != 2*len(resources)+1 {
		return Snapshot{}, errors.New("invalid media grant state response")
	}
	result := Snapshot{Epoch: values[0], Versions: make(map[string]string, len(resources)), Blocked: make(map[string]string, len(resources))}
	for i, resource := range resources {
		result.Versions[resource] = values[2*i+1]
		result.Blocked[resource] = values[2*i+2]
	}
	return result, nil
}

func AssetResource(id int64) string { return fmt.Sprintf("asset:%d", id) }
func FileResource(value string) string {
	if parsed, err := cid.Decode(value); err == nil {
		value = cid.NewCidV1(parsed.Type(), parsed.Hash()).String()
	}
	return "file:" + Session(value)
}

func (s *State) Check(ctx context.Context, c Claims) error {
	asset, file := AssetResource(c.AssetID), FileResource(c.SourceCID)
	state, err := s.Snapshot(ctx, []string{asset, file})
	if err != nil {
		return err
	}
	if state.Blocked[asset] != "" || state.Blocked[file] != "" || state.Epoch != c.Epoch || state.Versions[asset] != c.AssetVersion || state.Versions[file] != c.FileVersion {
		return ErrRevoked
	}
	return nil
}

// Begin serializes changes to an immutable-resource registration. A guard has
// no TTL: expiring a lock while its database writer is still running could
// authorize stale bytes. On a crash, an operator must finish/abort that writer
// before clearing the guard. Other resources continue to work normally.
var beginScript = redis.NewScript(`
if not redis.call('GET', KEYS[1]) then return redis.error_reply('MEDIA_EPOCH_MISSING') end
if redis.call('EXISTS', KEYS[3]) == 1 then return 0 end
redis.call('SET', KEYS[3], ARGV[1])
redis.call('SET', KEYS[2], ARGV[1])
return 1`)

var finishScript = redis.NewScript(`
if redis.call('GET', KEYS[2]) ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2])
redis.call('DEL', KEYS[2])
return 1`)

func (s *State) Begin(ctx context.Context, resource string) (func(context.Context) error, error) {
	version, err := randomVersion()
	if err != nil {
		return nil, err
	}
	key := s.prefix + resource
	result, err := beginScript.Run(ctx, s.client, []string{s.prefix + "epoch", key + ":version", key + ":guard"}, version).Int()
	if err != nil {
		return nil, err
	}
	if result != 1 {
		return nil, errors.New("media resource is being changed")
	}
	return func(ctx context.Context) error {
		next, err := randomVersion()
		if err != nil {
			return err
		}
		result, err := finishScript.Run(ctx, s.client, []string{key + ":version", key + ":guard"}, version, next).Int()
		if err != nil {
			return err
		}
		if result != 1 {
			return errors.New("media mutation guard was lost")
		}
		return nil
	}, nil
}

// Revoke permanently withdraws a physically deleted manifest across storage
// replicas. Restoring that CID is an explicit administrative operation; a
// normal grant refresh cannot resurrect bytes queued for unpinning.
func (s *State) Revoke(ctx context.Context, resource string) error {
	version, err := randomVersion()
	if err != nil {
		return err
	}
	return s.client.Eval(ctx, `
if not redis.call('GET', KEYS[1]) then return redis.error_reply('MEDIA_EPOCH_MISSING') end
redis.call('SET', KEYS[2], ARGV[1])
redis.call('SET', KEYS[3], 'deleted')
return 1`, []string{s.prefix + "epoch", s.prefix + resource + ":version", s.prefix + resource + ":guard"}, version).Err()
}
