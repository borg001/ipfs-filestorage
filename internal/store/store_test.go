package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAddGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-store.json")
	s, err := NewUnpinStore(path)
	if err != nil {
		t.Fatal(err)
	}

	masterCID := "QmMaster1"
	allCIDs := []string{"QmMaster1", "QmLow", "QmHigh", "QmSeg0"}

	s.AddGroup(masterCID, allCIDs)

	for _, cid := range allCIDs {
		if !s.Has(cid) {
			t.Errorf("Has(%q) = false, expected true after AddGroup", cid)
		}
	}

	group := s.GetGroup(masterCID)
	if len(group) != len(allCIDs) {
		t.Errorf("GetGroup returned %d CIDs, want %d", len(group), len(allCIDs))
	}
}

func TestGetGroupNonExistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-store.json")
	s, _ := NewUnpinStore(path)

	group := s.GetGroup("QmNonExistent")
	if group != nil {
		t.Errorf("GetGroup for non-existent master = %v, want nil", group)
	}
}

func TestRemoveGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-store.json")
	s, _ := NewUnpinStore(path)

	masterCID := "QmMaster2"
	allCIDs := []string{"QmMaster2", "QmV1", "QmV2", "QmSeg1"}

	s.AddGroup(masterCID, allCIDs)
	s.RemoveGroup(masterCID)

	for _, cid := range allCIDs {
		if s.Has(cid) {
			t.Errorf("Has(%q) = true after RemoveGroup, want false", cid)
		}
	}

	if s.GetGroup(masterCID) != nil {
		t.Error("GetGroup should return nil after RemoveGroup")
	}
}

func TestRemoveGroupPartialOverlap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-store.json")
	s, _ := NewUnpinStore(path)

	s.AddGroup("QmA", []string{"QmA", "QmB", "QmC"})
	s.AddGroup("QmD", []string{"QmD", "QmB", "QmE"})

	s.RemoveGroup("QmD")

	if !s.Has("QmA") {
		t.Error("QmA should still be in unpin list")
	}
}

func TestHasNonExistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-store.json")
	s, _ := NewUnpinStore(path)

	if s.Has("QmNothing") {
		t.Error("Has on empty store should return false")
	}
}

func TestExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-store.json")
	s, _ := NewUnpinStore(path)

	now := time.Now().UTC()
	s.entries["QmOld"] = now.Add(-2 * time.Hour)
	s.entries["QmNew"] = now.Add(-30 * time.Minute)
	s.saveUnsafe()

	cutoff := now.Add(-1 * time.Hour)
	expired := s.Expired(cutoff)

	if len(expired) != 1 || expired[0] != "QmOld" {
		t.Errorf("Expired() = %v, want [QmOld]", expired)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "persist-store.json")

	s1, _ := NewUnpinStore(path)
	s1.AddGroup("QmMaster3", []string{"QmMaster3", "QmVid1"})

	s2, _ := NewUnpinStore(path)

	if !s2.Has("QmMaster3") {
		t.Error("QmMaster3 should persist across store reload")
	}
	if !s2.Has("QmVid1") {
		t.Error("QmVid1 should persist across store reload")
	}
	group := s2.GetGroup("QmMaster3")
	if len(group) != 2 {
		t.Errorf("GetGroup after reload: len = %d, want 2", len(group))
	}
}

func TestLegacyFormatLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy-store.json")

	legacy := `[{"cid":"QmLegacy1","unpinned_at":"2025-01-01T00:00:00Z"},{"cid":"QmLegacy2","unpinned_at":"2025-01-01T00:00:00Z"}]`
	os.WriteFile(path, []byte(legacy), 0644)

	s, err := NewUnpinStore(path)
	if err != nil {
		t.Fatalf("Failed to load legacy format: %v", err)
	}

	if !s.Has("QmLegacy1") {
		t.Error("Legacy entry QmLegacy1 not loaded")
	}
	if !s.Has("QmLegacy2") {
		t.Error("Legacy entry QmLegacy2 not loaded")
	}
	if len(s.GetGroup("QmLegacy1")) != 0 {
		t.Error("Legacy format should have no groups")
	}
}

func TestAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-store.json")
	s, _ := NewUnpinStore(path)

	s.Add("QmA")
	s.Add("QmB")

	all := s.All()
	if len(all) != 2 {
		t.Errorf("All() returned %d entries, want 2", len(all))
	}
}
