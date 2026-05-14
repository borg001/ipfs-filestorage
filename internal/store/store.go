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
	groups   map[string][]string  // masterCID → все связанные CID
}

type Entry struct {
	CID        string    `json:"cid"`
	UnpinnedAt time.Time `json:"unpinned_at"`
}

func NewUnpinStore(path string) (*UnpinStore, error) {
	s := &UnpinStore{
		path:   path,
		entries: make(map[string]time.Time),
		groups:  make(map[string][]string),
	}
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

// AddGroup добавляет master CID и все связанные CID в unpin-список.
// При удалении по master CID будут анпиннуты все чанки и плейлисты.
func (s *UnpinStore) AddGroup(masterCID string, allCIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.groups[masterCID] = allCIDs
	// Помечаем все CID из группы
	for _, cid := range allCIDs {
		s.entries[cid] = now
	}
	s.saveUnsafe()
}

// GetGroup возвращает все CID связанные с master CID.
func (s *UnpinStore) GetGroup(masterCID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.groups[masterCID]
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
	delete(s.groups, cid)
	s.saveUnsafe()
}

// RemoveGroup удаляет master CID и все связанные CID из хранилища.
func (s *UnpinStore) RemoveGroup(masterCID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cids, ok := s.groups[masterCID]; ok {
		for _, cid := range cids {
			delete(s.entries, cid)
		}
	}
	delete(s.groups, masterCID)
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

	var raw struct {
		Entries []Entry   `json:"entries"`
		Groups  map[string][]string `json:"groups"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Попытка загрузить в старом формате (массив Entry)
		var oldEntries []Entry
		if err2 := json.Unmarshal(data, &oldEntries); err2 != nil {
			return err
		}
		raw.Entries = oldEntries
		raw.Groups = make(map[string][]string)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]time.Time, len(raw.Entries))
	for _, e := range raw.Entries {
		s.entries[e.CID] = e.UnpinnedAt
	}
	s.groups = raw.Groups
	if s.groups == nil {
		s.groups = make(map[string][]string)
	}
	return nil
}

func (s *UnpinStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnsafe()
}

func (s *UnpinStore) saveUnsafe() error {
	raw := struct {
		Entries []Entry   `json:"entries"`
		Groups  map[string][]string `json:"groups"`
	}{
		Entries: make([]Entry, 0, len(s.entries)),
		Groups:  s.groups,
	}
	for cid, t := range s.entries {
		raw.Entries = append(raw.Entries, Entry{CID: cid, UnpinnedAt: t})
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}
