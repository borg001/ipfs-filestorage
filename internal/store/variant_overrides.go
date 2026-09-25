package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// VariantOverrides says which file now stands in for a variant computed at
// upload. A bundle is addressed by its content, so a variant computed again
// by newer code - a face mask that fades into the photo instead of ending on
// a hard line - cannot go back into the bundle without changing the address
// every profile, post and chat already points at. The new file is added on
// its own, and the old variant is mapped to it here.
type VariantOverrides struct {
	mu      sync.RWMutex
	path    string
	entries map[string]string
}

// VariantOverrideKey names a variant inside a bundle, or a stand-alone file
// when variant is empty (a video poster rendition is a file of its own).
func VariantOverrideKey(cid, variant string) string {
	if variant == "" {
		return cid
	}
	return cid + "/" + variant
}

func NewVariantOverrides(path string) (*VariantOverrides, error) {
	s := &VariantOverrides{path: path, entries: make(map[string]string)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read variant overrides: %w", err)
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s.entries); err != nil {
		return nil, fmt.Errorf("decode variant overrides: %w", err)
	}
	return s, nil
}

// Get returns the file that replaces key. A missing store replaces nothing.
func (s *VariantOverrides) Get(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cid, ok := s.entries[key]
	return cid, ok && cid != ""
}

// Set records the replacement and writes the whole map through a temporary
// file, so a crash never leaves half a map behind.
func (s *VariantOverrides) Set(key, cid string) error {
	if s == nil {
		return fmt.Errorf("variant overrides are not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = cid
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".variant-overrides-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
