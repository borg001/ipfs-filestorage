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

type mockAdder struct {
	mu        sync.Mutex
	callCount int
	errAfter  int
	errMsg    string
}

func (m *mockAdder) Add(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error) {
	m.mu.Lock()
	m.callCount++
	cnt := m.callCount
	m.mu.Unlock()

	if m.errAfter > 0 && cnt > m.errAfter {
		return nil, fmt.Errorf(m.errMsg)
	}

	io.ReadAll(r)
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

	_, err := u.UploadDir(context.Background(), outputDir)
	if err == nil {
		t.Error("Expected error for empty directory")
	}
}

func TestUploadDirNilAdder(t *testing.T) {
	u := NewUploader(nil)
	outputDir := t.TempDir()
	os.WriteFile(filepath.Join(outputDir, "test.m4s"), []byte("data"), 0644)

	_, err := u.UploadDir(context.Background(), outputDir)
	if err == nil {
		t.Error("Expected error for nil adder")
	}
}

func TestUploadDirWithFiles(t *testing.T) {
	outputDir := t.TempDir()
	lowDir := filepath.Join(outputDir, "low")
	medDir := filepath.Join(outputDir, "medium")
	highDir := filepath.Join(outputDir, "high")
	postersDir := filepath.Join(outputDir, "posters")

	os.MkdirAll(lowDir, 0755)
	os.MkdirAll(medDir, 0755)
	os.MkdirAll(highDir, 0755)
	os.MkdirAll(postersDir, 0755)
	os.MkdirAll(filepath.Join(postersDir, "blur"), 0755)
	os.MkdirAll(filepath.Join(postersDir, "blur_faces"), 0755)

	playlist := "#EXTM3U\n#EXT-X-VERSION:6\n#EXTINF:4.000,\nseg_0.m4s\n#EXT-X-ENDLIST\n"
	os.WriteFile(filepath.Join(lowDir, "playlist.m3u8"), []byte(playlist), 0644)
	os.WriteFile(filepath.Join(lowDir, "seg_0.m4s"), []byte("fake-segment-data-low"), 0644)
	os.WriteFile(filepath.Join(medDir, "playlist.m3u8"), []byte(playlist), 0644)
	os.WriteFile(filepath.Join(medDir, "seg_0.m4s"), []byte("fake-segment-data-med"), 0644)
	os.WriteFile(filepath.Join(highDir, "playlist.m3u8"), []byte(playlist), 0644)
	os.WriteFile(filepath.Join(highDir, "seg_0.m4s"), []byte("fake-segment-data-high"), 0644)
	os.WriteFile(filepath.Join(postersDir, "180x320.jpg"), []byte("fake-poster-small"), 0644)
	os.WriteFile(filepath.Join(postersDir, "720x1280.jpg"), []byte("fake-poster-large"), 0644)
	os.WriteFile(filepath.Join(postersDir, "blur", "180x320.jpg"), []byte("fake-blur-poster-small"), 0644)
	os.WriteFile(filepath.Join(postersDir, "blur_faces", "180x320.jpg"), []byte("fake-face-blur-poster-small"), 0644)

	adder := &mockAdder{}
	u := NewUploader(adder)
	result, err := u.UploadDir(context.Background(), outputDir)
	if err != nil {
		t.Fatalf("UploadDir failed: %v", err)
	}

	if result.MasterCID == "" {
		t.Error("MasterCID should not be empty")
	}
	if len(result.VariantCIDs) != 3 {
		t.Errorf("Expected 3 variant CIDs, got %d", len(result.VariantCIDs))
	}
	if len(result.AllCIDs) < 7 {
		t.Errorf("Expected at least 7 allCIDs, got %d", len(result.AllCIDs))
	}
	if len(result.SegmentCIDs) != 3 {
		t.Errorf("Expected 3 segment CIDs, got %d", len(result.SegmentCIDs))
	}
	if len(result.PosterCIDs) != 2 {
		t.Errorf("Expected 2 poster CIDs, got %d", len(result.PosterCIDs))
	}
	if result.PosterCIDs["180x320"] == "" {
		t.Error("Expected 180x320 poster CID")
	}
	if result.PrivacyPosterCIDs["blur"]["180x320"] == "" {
		t.Errorf("Expected blur privacy poster CID, got %+v", result.PrivacyPosterCIDs)
	}
	if result.PrivacyPosterCIDs["blur_faces"]["180x320"] == "" {
		t.Errorf("Expected blur_faces privacy poster CID, got %+v", result.PrivacyPosterCIDs)
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
	result, err := u.UploadDir(context.Background(), outputDir)
	if err != nil {
		t.Fatalf("UploadDir with only segments failed: %v", err)
	}

	if len(result.SegmentCIDs) != 2 {
		t.Errorf("Expected 2 segment CIDs, got %d", len(result.SegmentCIDs))
	}
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
	_, err := u.UploadDir(context.Background(), outputDir)
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

func TestRewriteVariantPlaylistInitMapSubstitution(t *testing.T) {
	outputDir := t.TempDir()
	lowDir := filepath.Join(outputDir, "low")
	os.MkdirAll(lowDir, 0755)

	playlist := "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4.000,\nseg_0.m4s\n#EXT-X-ENDLIST\n"
	os.WriteFile(filepath.Join(lowDir, "playlist.m3u8"), []byte(playlist), 0644)

	fileCIDs := map[string]string{
		"low/init.mp4":  "QmInit",
		"low/seg_0.m4s": "QmSeg0",
	}

	u := NewUploader(nil)
	result, err := u.rewriteVariantPlaylist(outputDir, "low/playlist.m3u8", fileCIDs)
	if err != nil {
		t.Fatalf("rewriteVariantPlaylist failed: %v", err)
	}

	if !strings.Contains(result, "#EXT-X-MAP:URI=\"QmInit.mp4\"") {
		t.Error("Rewritten playlist should replace init map with CID")
	}
	if strings.Contains(result, "URI=\"init.mp4\"") {
		t.Error("Rewritten playlist should NOT contain original init.mp4 URI")
	}
}

func TestRewriteVariantPlaylistMissingSegment(t *testing.T) {
	outputDir := t.TempDir()
	lowDir := filepath.Join(outputDir, "low")
	os.MkdirAll(lowDir, 0755)

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
	if !strings.Contains(content, "../segment/QmLow.m3u8") {
		t.Error("low variant URL should point to m3u8 playlist")
	}
	if strings.Contains(content, "../segment/QmLow\n") {
		t.Error("variant URL should not omit m3u8 extension")
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

func TestBuildMasterPlaylistPosters(t *testing.T) {
	u := NewUploader(nil)
	variantCIDs := map[string]string{
		"low": "QmLow",
	}
	fileCIDs := map[string]string{
		"posters/720x1280.jpg":           "QmPosterLarge",
		"posters/180x320.jpg":            "QmPosterSmall",
		"posters/blur/180x320.jpg":       "QmPosterBlur",
		"posters/blur_faces/180x320.jpg": "QmPosterFaceBlur",
		"low/seg_0.m4s":                  "QmSeg",
	}

	content, err := u.buildMasterPlaylist(variantCIDs, fileCIDs)
	if err != nil {
		t.Fatalf("buildMasterPlaylist failed: %v", err)
	}

	smallIdx := strings.Index(content, `#EXT-X-IAMFREE-POSTER:SIZE=180x320,URI="../segment/QmPosterSmall.jpg"`)
	largeIdx := strings.Index(content, `#EXT-X-IAMFREE-POSTER:SIZE=720x1280,URI="../segment/QmPosterLarge.jpg"`)
	streamIdx := strings.Index(content, "#EXT-X-STREAM-INF")
	if smallIdx < 0 {
		t.Error("Missing small poster tag")
	}
	if largeIdx < 0 {
		t.Error("Missing large poster tag")
	}
	if smallIdx >= largeIdx {
		t.Error("Small poster should come before large poster")
	}
	if largeIdx >= streamIdx {
		t.Error("Poster tags should come before stream variants")
	}
	if !strings.Contains(content, `#EXT-X-IAMFREE-POSTER:VARIANT=blur,SIZE=180x320,URI="../segment/QmPosterBlur.jpg"`) {
		t.Error("Missing blur privacy poster tag")
	}
	if !strings.Contains(content, `#EXT-X-IAMFREE-POSTER:VARIANT=blur_faces,SIZE=180x320,URI="../segment/QmPosterFaceBlur.jpg"`) {
		t.Error("Missing blur_faces privacy poster tag")
	}
}

func TestPosterFromPath(t *testing.T) {
	tests := []struct {
		path    string
		variant string
		size    string
		ok      bool
	}{
		{path: "posters/180x320.jpg", size: "180x320", ok: true},
		{path: "posters/blur/180x320.jpg", variant: "blur", size: "180x320", ok: true},
		{path: "posters/blur_faces/720x1280.jpg", variant: "blur_faces", size: "720x1280", ok: true},
		{path: "posters/blur/extra/180x320.jpg", ok: false},
		{path: "posters/180x320.png", ok: false},
	}
	for _, tt := range tests {
		poster, ok := posterFromPath(tt.path)
		if ok != tt.ok || poster.variant != tt.variant || poster.size != tt.size {
			t.Errorf("posterFromPath(%q) = %+v, %t; want variant=%q size=%q ok=%t", tt.path, poster, ok, tt.variant, tt.size, tt.ok)
		}
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

	result2 := replaceInSlice(s, "z", "y")
	if len(result2) != 3 {
		t.Errorf("replaceInSlice with missing: got len %d, want 3", len(result2))
	}
}

func TestReplaceInSliceDedup(t *testing.T) {
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
