package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/mediagrant"
)

type grantState interface {
	Check(context.Context, mediagrant.Claims) error
	Begin(context.Context, string) (func(context.Context) error, error)
	Revoke(context.Context, string) error
}

type mediaGrantVerifier struct {
	codec *mediagrant.Codec
	state grantState
}

func newMediaGrantVerifier(cfg config.MediaGrantConfig) (*mediaGrantVerifier, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	var keys map[string]string
	if err := json.Unmarshal([]byte(cfg.Keys), &keys); err != nil {
		return nil, errors.New("invalid MEDIA_GRANT_KEYS JSON")
	}
	codec, err := mediagrant.New(cfg.ActiveKey, cfg.Audience, keys)
	if err != nil {
		return nil, err
	}
	state, err := mediagrant.NewState(cfg.RedisURL, cfg.Namespace)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Only the API initializes the epoch. Starting a downloader must not
	// silently re-enable stale grants after state loss.
	if _, err := state.Snapshot(ctx, nil); err != nil {
		state.Close()
		return nil, err
	}
	return &mediaGrantVerifier{codec: codec, state: state}, nil
}

func mediaGrantToken(r *http.Request) string {
	if value := r.Header.Get("Authorization"); value != "" {
		if strings.HasPrefix(value, "Bearer ") {
			return strings.TrimPrefix(value, "Bearer ")
		}
		return ""
	}
	if value := r.Header.Get("X-API-Key"); value != "" {
		return value
	}
	if value := r.URL.Query().Get("token"); value != "" {
		return value
	}
	return r.URL.Query().Get("access_token")
}

func (v *mediaGrantVerifier) resolve(r *http.Request, link string) (mediaDeliveryDecision, error) {
	if v == nil {
		return mediaDeliveryDecision{}, mediagrant.ErrInvalid
	}
	values := r.URL.Query()["grant"]
	if len(values) != 1 || values[0] == "" {
		return mediaDeliveryDecision{}, mediagrant.ErrInvalid
	}
	claims, err := v.codec.Open(values[0], mediaGrantToken(r), time.Now())
	if err != nil {
		return mediaDeliveryDecision{}, err
	}
	if strconv.FormatInt(claims.LinkID, 10) != link {
		return mediaDeliveryDecision{}, mediagrant.ErrInvalid
	}
	operation := "image"
	if strings.HasPrefix(r.URL.Path, "/stream/link/") {
		operation = "stream"
		if strings.HasSuffix(r.URL.Path, "/poster.jpg") {
			operation = "poster"
		}
	}
	if !claims.Allows(operation) || strings.HasSuffix(r.URL.Path, "/bundle") {
		return mediaDeliveryDecision{}, mediagrant.ErrInvalid
	}
	if err := v.state.Check(r.Context(), claims); err != nil {
		return mediaDeliveryDecision{}, err
	}
	return mediaDeliveryDecision{Mode: mediaDeliveryMode(claims.Mode), Managed: true, SourceCID: claims.SourceCID, SourceIsVideo: claims.Video, PosterCID: claims.PosterCID}, nil
}
