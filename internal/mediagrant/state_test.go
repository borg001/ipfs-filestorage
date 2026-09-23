package mediagrant

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ipfs/go-cid"
)

func TestRedisRevocationAndMutationRaces(t *testing.T) {
	address := os.Getenv("MEDIA_REDIS_TEST_URL")
	if address == "" {
		t.Skip("set MEDIA_REDIS_TEST_URL")
	}
	namespace, err := randomVersion()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewState(address, namespace)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	defer s.Close()
	defer func() {
		keys, err := s.client.Keys(ctx, s.prefix+"*").Result()
		if err == nil && len(keys) > 0 {
			s.client.Del(ctx, keys...)
		}
	}()
	if _, err := s.Snapshot(ctx, nil); err == nil {
		t.Fatal("missing epoch must fail closed")
	}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	claim := testClaims()
	asset, file := AssetResource(claim.AssetID), FileResource(claim.SourceCID)
	before, err := s.Snapshot(ctx, []string{asset, file})
	if err != nil {
		t.Fatal(err)
	}
	claim.Epoch = before.Epoch
	claim.AssetVersion = before.Versions[asset]
	claim.FileVersion = before.Versions[file]
	if err := s.Check(ctx, claim); err != nil {
		t.Fatal(err)
	}
	finish, err := s.Begin(ctx, asset)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(ctx, asset); err == nil {
		t.Fatal("concurrent writer acquired the same guard")
	}
	if err := s.Check(ctx, claim); err == nil {
		t.Fatal("download accepted during mutation")
	}
	locked, err := s.Snapshot(ctx, []string{asset, file})
	if err != nil {
		t.Fatal(err)
	}
	if locked.Blocked[asset] == "" || locked.Blocked[file] != "" {
		t.Fatal("mutation did not isolate the affected resource")
	}
	if err := finish(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(ctx, claim); !errors.Is(err, ErrRevoked) {
		t.Fatalf("old grant: %v", err)
	}
	after, err := s.Snapshot(ctx, []string{asset, file})
	if err != nil {
		t.Fatal(err)
	}
	if after.Versions[asset] == before.Versions[asset] {
		t.Fatal("mutation failed to advance version")
	}
	claim.AssetVersion = after.Versions[asset]
	if err := s.Check(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if err := s.client.Del(ctx, s.prefix+"epoch").Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(ctx, claim); err == nil {
		t.Fatal("state loss authorized old grant")
	}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(ctx, claim); !errors.Is(err, ErrRevoked) {
		t.Fatalf("epoch reset: %v", err)
	}
	if err := s.Revoke(ctx, file); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, file); err != nil {
		t.Fatal("delete must be idempotent", err)
	}
	deleted, err := s.Snapshot(ctx, []string{file})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Blocked[file] != "deleted" {
		t.Fatal("deleted file became grantable")
	}
}

func TestRevocationUsesCanonicalCID(t *testing.T) {
	v0 := "QmYwAPJzv5CZsnAzt8auVTLk23ktbJzdDdekkjbuWAvLRG"
	parsed, err := cid.Decode(v0)
	if err != nil {
		t.Fatal(err)
	}
	v1 := cid.NewCidV1(parsed.Type(), parsed.Hash()).String()
	if v0 == v1 || FileResource(v0) != FileResource(v1) {
		t.Fatal("CID aliases must share revocation state")
	}
}
