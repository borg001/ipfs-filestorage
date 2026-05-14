package video

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/ipfs"
)

// mockAdder реализует IPFSAdder для тестов
type mockAdder struct {
	addFunc func(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error)
}

func (m *mockAdder) Add(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error) {
	if m.addFunc != nil {
		return m.addFunc(ctx, filename, r)
	}
	data, _ := io.ReadAll(r)
	cid := fmt.Sprintf("QmMock%d%x", len(data), len(data))
	return &ipfs.AddResult{CID: cid, Name: filename}, nil
}

func TestUploadDirEmpty(t *testing.T) {
	u := NewUploader(nil)
	outputDir := t.TempDir()

	_, err := u.UploadDir(t.Context(), outputDir)
	if err == nil {
		t.Error("Expected error for empty directory")
	}
}

func TestUploadDirWithFiles(t *testing.T) {
	// Создаём структуру HLS
	outputDir := t.TempDir()
	lowDir := outputDir + "/low"
	medDir := outputDir + "/medium"
	highDir := outputDir + "/high"

	os.MkdirAll(lowDir, 0755)
	os.MkdirAll(medDir, 0755)
	os.MkdirAll(highDir, 0755)

	os.WriteFile(lowDir+"/playlist.m3u8", []byte("#EXTM3U\n#EXTINF:4,\nseg_0.m4s\n#EXT-X-ENDLIST\n"), 0644)
	os.WriteFile(lowDir+"/seg_0.m4s", []byte("fake-segment-data-low"), 0644)
	os.WriteFile(medDir+"/playlist.m3u8", []byte("#EXTM3U\n#EXTINF:4,\nseg_0.m4s\n#EXT-X-ENDLIST\n"), 0644)
	os.WriteFile(medDir+"/seg_0.m4s", []byte("fake-segment-data-med"), 0644)
	os.WriteFile(highDir+"/playlist.m3u8", []byte("#EXTM3U\n#EXTINF:4,\nseg_0.m4s\n#EXT-X-ENDLIST\n"), 0644)
	os.WriteFile(highDir+"/seg_0.m4s", []byte("fake-segment-data-high"), 0644)

	callCount := 0
	adder := &mockAdder{
		addFunc: func(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error) {
			callCount++
			data, _ := io.ReadAll(r)
			cid := fmt.Sprintf("QmFile%d", callCount)
			return &ipfs.AddResult{CID: cid, Name: filename}, nil
		},
	}

	u := &Uploader{adder: adder}
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
	if len(result.AllCIDs) < 7 { // 3 playlists + 3 segments + 1 master
		t.Errorf("Expected at least 7 allCIDs, got %d", len(result.AllCIDs))
	}
	if len(result.SegmentCIDs) != 3 {
		t.Errorf("Expected 3 segment CIDs, got %d", len(result.SegmentCIDs))
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

	// Порядок: low → medium → high
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
		// medium отсутствует
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
