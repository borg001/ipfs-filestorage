package store

import (
	"path/filepath"
	"testing"
)

func TestVariantOverridesSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "variant-overrides.json")
	overrides, err := NewVariantOverrides(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := overrides.Get(VariantOverrideKey("QmBundle", "blur_faces")); ok {
		t.Fatal("an empty store replaced a variant")
	}
	if err := overrides.Set(VariantOverrideKey("QmBundle", "blur_faces"), "QmSoftMask"); err != nil {
		t.Fatal(err)
	}
	if err := overrides.Set(VariantOverrideKey("QmPosterMask", ""), "QmSoftPoster"); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewVariantOverrides(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.Get("QmBundle/blur_faces"); !ok || got != "QmSoftMask" {
		t.Fatalf("bundle variant = %q, %v", got, ok)
	}
	if got, ok := reopened.Get("QmPosterMask"); !ok || got != "QmSoftPoster" {
		t.Fatalf("poster rendition = %q, %v", got, ok)
	}
	var missing *VariantOverrides
	if _, ok := missing.Get("QmBundle/blur_faces"); ok {
		t.Fatal("a missing store replaced a variant")
	}
}
