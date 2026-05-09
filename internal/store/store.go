package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// UnpinStore хранит CID файлов, помеченных для удаления
type UnpinStore struct {
	mu       sync.RWMutex
	path     string
	entries  map[string]time.Time // CID → время пометки
}

type Entry struct {
	CID        string    `json:"cid"`
	UnpinnedAt time.Time `json:"unpinned_at"`
}

func NewUnpinStore(path string) (*UnpinStore, error) {
	s := &UnpinStore{path: path, entries: make(map[string]time.Time)}
	if err := s.Load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load unpin store: %w", err)
	}
	return s, nil
}

func (s *UnpinStore) Add(cid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[cid] = time.Now().UTC()
	s.saveUnsafe()
}

func (s *UnpinStore) Has(cid string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.entries[cid]
	return ok
}

func (s *UnpinStore) Remove(cid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, cid)
	s.saveUnsafe()
}

func (s *UnpinStore) Expired(cutoff time.Time) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []string
	for cid, t := range s.entries {
		if t.Before(cutoff) {
			result = append(result, cid)
		}
	}
	return result
}

func (s *UnpinStore) All() map[string]time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]time.Time, len(s.entries))
	for k, v := range s.entries {
		out[k] = v
	}
	return out
}

func (s *UnpinStore) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]time.Time, len(entries))
	for _, e := range entries {
		s.entries[e.CID] = e.UnpinnedAt
	}
	return nil
}

func (s *UnpinStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnsafe()
}

func (s *UnpinStore) saveUnsafe() error {
	entries := make([]Entry, 0, len(s.entries))
	for cid, t := range s.entries {
		entries = append(entries, Entry{CID: cid, UnpinnedAt: t})
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}
