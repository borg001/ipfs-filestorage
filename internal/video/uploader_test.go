package video

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/ipfs"
)

// mockAdder реализует IPFSAdder для тестов
type mockAdder struct {
	mu        sync.Mutex
	callCount int
	errAfter  int    // вернуть ошибку после N вызовов (0 = никогда)
	errMsg    string // текст ошибки
}

func (m *mockAdder) Add(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error) {
	m.mu.Lock()
	m.callCount++
	cnt := m.callCount
	m.mu.Unlock()

	if m.errAfter > 0 && cnt > m.errAfter {
		return nil, fmt.Errorf(m.errMsg)
	}

	data, _ := io.ReadAll(r)
	cid := fmt.Sprintf("QmFile%d", cnt)
	return &ipfs.AddResult{CID: cid, Name: filename}, nil
}

func (m *mockAdder) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func TestUploadDirEmpty(t *testing.T) {
	u := NewUploader(nil)
	outputDir := t.TempDir()

	_, err := u.UploadDir(t.Context(), outputDir)
	if err == nil {
		t.Error("Expected error for empty directory")
	}
}

func TestUploadDirNilAdder(t *testing.T) {
	u := NewUploader(nil)
	outputDir := t.TempDir()
	os.WriteFile(filepath.Join(outputDir, "test.m4s"), []byte("data"), 0644)

	_, err := u.UploadDir(t.Context(), outputDir)
	if err == nil {
		t.Error("Expected error for nil adder")
	}
}

func TestUploadDirWithFiles(t *testing.T) {
	outputDir := t.TempDir()
	lowDir := filepath.Join(outputDir, "low")
	medDir := filepath.Join(outputDir, "medium")
	highDir := filepath.Join(outputDir, "high")

	os.MkdirAll(lowDir, 0755)
	os.MkdirAll(medDir, 0755)
	os.MkdirAll(highDir, 0755)

	playlist := "#EXTM3U\n#EXT-X-VERSION:6\n#EXTINF:4.000,\nseg_0.m4s\n#EXT-X-ENDLIST\n"
	os.WriteFile(filepath.Join(lowDir, "playlist.m3u8"), []byte(playlist), 0644)
	os.WriteFile(filepath.Join(lowDir, "seg_0.m4s"), []byte("fake-segment-data-low"), 0644)
	os.WriteFile(filepath.Join(medDir, "playlist.m3u8"), []byte(playlist), 0644)
	os.WriteFile(filepath.Join(medDir, "seg_0.m4s"), []byte("fake-segment-data-med"), 0644)
	os.WriteFile(filepath.Join(highDir, "playlist.m3u8"), []byte(playlist), 0644)
	os.WriteFile(filepath.Join(highDir, "seg_0.m4s"), []byte("fake-segment-data-high"), 0644)

	adder := &mockAdder{}
	u := NewUploader(adder)
	result, err := u.UploadDir(t.Context(), outputDir)
	if err != nil {
		t.Fatalf("UploadDir failed: %v", err)
	}

	if result.MasterCID == "" {
		t.Error("MasterCID should not be empty")
	}
	if len(result.VariantCIDs) != 3 {
		t.Errorf("Expected 3 variant CIDs, got %d", len(result.VariantCIDs))
	}
	if len(result.AllCIDs) < 7 { // 3 playlists + 3 segments + 1 master = 7
		t.Errorf("Expected at least 7 allCIDs, got %d", len(result.AllCIDs))
	}
	if len(result.SegmentCIDs) != 3 {
		t.Errorf("Expected 3 segment CIDs, got %d", len(result.SegmentCIDs))
	}
	if adder.getCallCount() < 7 {
		t.Errorf("Expected at least 7 Add calls, got %d", adder.getCallCount())
	}
}

func TestUploadDirOnlySegments(t *testing.T) {
	outputDir := t.TempDir()
	os.WriteFile(filepath.Join(outputDir, "seg_0.m4s"), []byte("segment-data"), 0644)
	os.WriteFile(filepath.Join(outputDir, "seg_1.m4s"), []byte("segment-data-2"), 0644)

	adder := &mockAdder{}
	u := NewUploader(adder)
	result, err := u.UploadDir(t.Context(), outputDir)
	if err != nil {
		t.Fatalf("UploadDir with only segments failed: %v", err)
	}

	if len(result.SegmentCIDs) != 2 {
		t.Errorf("Expected 2 segment CIDs, got %d", len(result.SegmentCIDs))
	}
	// Master playlist should still be generated
	if result.MasterCID == "" {
		t.Error("MasterCID should not be empty even without variant playlists")
	}
}

func TestUploadDirAdderError(t *testing.T) {
	outputDir := t.TempDir()
	lowDir := filepath.Join(outputDir, "low")
	os.MkdirAll(lowDir, 0755)
	os.WriteFile(filepath.Join(lowDir, "seg_0.m4s"), []byte("data"), 0644)

	adder := &mockAdder{errAfter: 1, errMsg: "ipfs connection refused"}
	u := NewUploader(adder)
	_, err := u.UploadDir(t.Context(), outputDir)
	if err == nil {
		t.Error("Expected error when adder fails")
	}
	if !strings.Contains(err.Error(), "ipfs connection refused") {
		t.Errorf("Error should contain original message, got: %v", err)
	}
}

func TestRewriteVariantPlaylistCIDSubstitution(t *testing.T) {
	outputDir := t.TempDir()
	lowDir := filepath.Join(outputDir, "low")
	os.MkdirAll(lowDir, 0755)

	playlist := "#EXTM3U\n#EXT-X-VERSION:6\n#EXTINF:4.000,\nseg_0.m4s\n#EXTINF:4.000,\nseg_1.m4s\n#EXT-X-ENDLIST\n"
	os.WriteFile(filepath.Join(lowDir, "playlist.m3u8"), []byte(playlist), 0644)

	fileCIDs := map[string]string{
		"low/seg_0.m4s": "QmSeg0",
		"low/seg_1.m4s": "QmSeg1",
	}

	u := NewUploader(nil)
	result, err := u.rewriteVariantPlaylist(outputDir, "low/playlist.m3u8", fileCIDs)
	if err != nil {
		t.Fatalf("rewriteVariantPlaylist failed: %v", err)
	}

	if !strings.Contains(result, "QmSeg0.m4s") {
		t.Error("Rewritten playlist should contain QmSeg0.m4s")
	}
	if !strings.Contains(result, "QmSeg1.m4s") {
		t.Error("Rewritten playlist should contain QmSeg1.m4s")
	}
	if strings.Contains(result, "seg_0.m4s\n") {
		t.Error("Rewritten playlist should NOT contain original seg_0.m4s")
	}
}

func TestRewriteVariantPlaylistMissingSegment(t *testing.T) {
	outputDir := t.TempDir()
	lowDir := filepath.Join(outputDir, "low")
	os.MkdirAll(lowDir, 0755)

	// Ссылка на seg_1.m4s, которого нет в fileCIDs — должна остаться как есть
	playlist := "#EXTM3U\n#EXTINF:4.000,\nseg_0.m4s\n#EXTINF:4.000,\nseg_2.m4s\n#EXT-X-ENDLIST\n"
	os.WriteFile(filepath.Join(lowDir, "playlist.m3u8"), []byte(playlist), 0644)

	fileCIDs := map[string]string{
		"low/seg_0.m4s": "QmSeg0",
	}

	u := NewUploader(nil)
	result, err := u.rewriteVariantPlaylist(outputDir, "low/playlist.m3u8", fileCIDs)
	if err != nil {
		t.Fatalf("rewriteVariantPlaylist failed: %v", err)
	}

	if !strings.Contains(result, "QmSeg0.m4s") {
		t.Error("Known segment should be replaced with CID")
	}
	// Неизвестный сегмент остаётся как есть
	if !strings.Contains(result, "seg_2.m4s") {
		t.Error("Unknown segment should remain as original filename")
	}
}

func TestBuildMasterPlaylistOrder(t *testing.T) {
	u := NewUploader(nil)
	variantCIDs := map[string]string{
		"low":    "QmLow",
		"medium": "QmMed",
		"high":   "QmHigh",
	}

	content, err := u.buildMasterPlaylist(variantCIDs, nil)
	if err != nil {
		t.Fatalf("buildMasterPlaylist failed: %v", err)
	}

	lowIdx := strings.Index(content, "QmLow")
	medIdx := strings.Index(content, "QmMed")
	highIdx := strings.Index(content, "QmHigh")

	if lowIdx >= medIdx {
		t.Error("low variant should come before medium")
	}
	if medIdx >= highIdx {
		t.Error("medium variant should come before high")
	}

	if !strings.Contains(content, "BANDWIDTH=500000") {
		t.Error("Missing low bandwidth")
	}
	if !strings.Contains(content, "BANDWIDTH=1500000") {
		t.Error("Missing medium bandwidth")
	}
	if !strings.Contains(content, "BANDWIDTH=4000000") {
		t.Error("Missing high bandwidth")
	}
}

func TestBuildMasterPlaylistMissingVariant(t *testing.T) {
	u := NewUploader(nil)
	variantCIDs := map[string]string{
		"low":  "QmLow",
		"high": "QmHigh",
	}

	content, err := u.buildMasterPlaylist(variantCIDs, nil)
	if err != nil {
		t.Fatalf("buildMasterPlaylist failed: %v", err)
	}

	if strings.Contains(content, "QmMed") {
		t.Error("Should not contain medium variant")
	}
	if !strings.Contains(content, "QmLow") {
		t.Error("Should contain low variant")
	}
	if !strings.Contains(content, "QmHigh") {
		t.Error("Should contain high variant")
	}
}

func TestReplaceInSlice(t *testing.T) {
	s := []string{"a", "b", "c"}
	result := replaceInSlice(s, "b", "x")
	if result[1] != "x" {
		t.Errorf("replaceInSlice: got %v, want x at index 1", result)
	}
	if len(result) != 3 {
		t.Errorf("replaceInSlice: got len %d, want 3", len(result))
	}

	// Замена несуществующего элемента — ничего не меняется
	result2 := replaceInSlice(s, "z", "y")
	if len(result2) != 3 {
		t.Errorf("replaceInSlice with missing: got len %d, want 3", len(result2))
	}
}

func TestReplaceInSliceDedup(t *testing.T) {
	// Проверяем что после rewrite AllCIDs обновляется корректно
	s := []string{"QmA", "QmOld", "QmB"}
	result := replaceInSlice(s, "QmOld", "QmNew")
	found := false
	for _, v := range result {
		if v == "QmNew" {
			found = true
		}
	}
	if !found {
		t.Error("QmNew should be present after replaceInSlice")
	}
}
