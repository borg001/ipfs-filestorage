package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConcurrentAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-store.json")
	s, err := NewUnpinStore(path)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	const goroutines = 50
	const cidsPerGoroutine = 10

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < cidsPerGoroutine; i++ {
				cid := "QmGoroutine" + string(rune('A'+gid)) + "_" + string(rune('0'+i))
				s.Add(cid)
			}
		}(g)
	}
	wg.Wait()

	all := s.All()
	if len(all) != goroutines*cidsPerGoroutine {
		t.Errorf("Expected %d entries after concurrent writes, got %d",
				goroutines*cidsPerGoroutine, len(all))
	}
}

func TestConcurrentAddAndHas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-rw-store.json")
	s, err := NewUnpinStore(path)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	for g := 0; g < 20; g++ {
		wg.Add(2)
		go func(gid int) {
			defer wg.Done()
			s.Add("QmCID" + string(rune('A'+gid)))
		}(g)
		go func(gid int) {
			defer wg.Done()
			s.Has("QmCID" + string(rune('A'+gid)))
		}(g)
	}
	wg.Wait()
}

func TestConcurrentAddAndRemoveGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-group-store.json")
	s, err := NewUnpinStore(path)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	for g := 0; g < 10; g++ {
		master := "QmMaster" + string(rune('A'+g))
		allCIDs := []string{master, master + "_v1", master + "_v2"}

		wg.Add(2)
		go func(m string, cids []string) {
			defer wg.Done()
			s.AddGroup(m, cids)
		}(master, allCIDs)
		go func(m string) {
			defer wg.Done()
			s.RemoveGroup(m)
		}(master)
	}
	wg.Wait()
}

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

	if !s.Has("QmB") {
		t.Error("QmB should still be in unpin list (part of QmA group)")
	}
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
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

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

func TestSaveEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty-store.json")

	s, _ := NewUnpinStore(path)
	err := s.Save()
	if err != nil {
		t.Fatalf("Save on empty store failed: %v", err)
	}

	s2, _ := NewUnpinStore(path)
	if len(s2.All()) != 0 {
		t.Error("Empty store should have 0 entries after reload")
	}
	if len(s2.GetGroup("anything")) != 0 {
		t.Error("Empty store should have 0 groups after reload")
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
