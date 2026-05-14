package video

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBitrateToName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"500k", "low"},
		{"700k", "low"},
		{"1500k", "medium"},
		{"2000k", "medium"},
		{"4000k", "high"},
		{"8000k", "high"},
		{"invalid", "invalid"},
	}

	for _, tt := range tests {
		got := bitrateToName(tt.input)
		if got != tt.want {
			t.Errorf("bitrateToName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMultiplyBitrate(t *testing.T) {
	tests := []struct {
		input  string
		factor int
		want   string
	}{
		{"500k", 2, "1000k"},
		{"1500k", 2, "3000k"},
		{"4000k", 3, "12000k"},
	}

	for _, tt := range tests {
		got := multiplyBitrate(tt.input, tt.factor)
		if got != tt.want {
			t.Errorf("multiplyBitrate(%q, %d) = %q, want %q", tt.input, tt.factor, got, tt.want)
		}
	}
}

func TestNewTranscoder(t *testing.T) {
	cfg := &VideoConfig{
		MaxDurationSec:     60,
		SegmentDurationSec: 4,
		Bitrates:           []string{"500k", "1500k", "4000k"},
		FFmpegPath:         "ffmpeg",
		FFprobePath:        "ffprobe",
		TempDir:            t.TempDir(),
	}

	tr := NewTranscoder(cfg)
	if tr == nil {
		t.Fatal("NewTranscoder returned nil")
	}
	if tr.cfg != cfg {
		t.Error("Transcoder config not set correctly")
	}
}

func TestNewValidator(t *testing.T) {
	cfg := &VideoConfig{
		MaxDurationSec:       60,
		MaxSizeBytes:         30 * 1024 * 1024,
		AspectRatioTolerance: 0.1,
		FFprobePath:          "ffprobe",
	}

	v := NewValidator(cfg)
	if v == nil {
		t.Fatal("NewValidator returned nil")
	}
	if v.cfg != cfg {
		t.Error("Validator config not set correctly")
	}
}

func TestNewUploader(t *testing.T) {
	u := NewUploader(nil) // nil client для теста конструктора
	if u == nil {
		t.Fatal("NewUploader returned nil")
	}
}

func TestBuildHLSArgs(t *testing.T) {
	cfg := &VideoConfig{
		SegmentDurationSec: 4,
		Bitrates:           []string{"500k", "1500k", "4000k"},
		FFmpegPath:         "ffmpeg",
		TempDir:            t.TempDir(),
	}

	tr := NewTranscoder(cfg)
	args := tr.buildHLSArgs("/tmp/input.mp4", "/tmp/output")

	// Проверяем ключевые аргументы
	hasInput := false
	hasHlsTime := false
	hasHlsSegmentType := false

	for i, arg := range args {
		if arg == "-i" && i+1 < len(args) && args[i+1] == "/tmp/input.mp4" {
			hasInput = true
		}
		if arg == "-hls_time" {
			hasHlsTime = true
		}
		if arg == "-hls_segment_type" {
			hasHlsSegmentType = true
		}
	}

	if !hasInput {
		t.Error("HLS args missing -i input")
	}
	if !hasHlsTime {
		t.Error("HLS args missing -hls_time")
	}
	if !hasHlsSegmentType {
		t.Error("HLS args missing -hls_segment_type (fMP4)")
	}

	// Проверяем, что для каждого битрейта есть свой выходной файл
	outputFiles := 0
	for _, arg := range args {
		if filepath.Ext(arg) == ".m3u8" {
			outputFiles++
		}
	}
	if outputFiles != 3 {
		t.Errorf("Expected 3 variant playlists in args, got %d", outputFiles)
	}
}

func TestTranscodeMissingInput(t *testing.T) {
	cfg := &VideoConfig{
		SegmentDurationSec: 4,
		Bitrates:           []string{"500k"},
		FFmpegPath:         "ffmpeg",
		FFprobePath:        "ffprobe",
		TempDir:            t.TempDir(),
		MaxDurationSec:     60,
	}

	tr := NewTranscoder(cfg)
	ctx := t.Context()

	_, err := tr.Transcode(ctx, "/nonexistent/input.mp4", t.TempDir())
	if err == nil {
		t.Error("Expected error for missing input file")
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
}

func TestUploadResult(t *testing.T) {
	// Проверяем структуру UploadResult
	result := &UploadResult{
		MasterCID:   "QmTest",
		VariantCIDs: map[string]string{"low": "QmLow", "high": "QmHigh"},
		SegmentCIDs: map[string]string{"low/seg_0.m4s": "QmSeg0"},
		AllCIDs:     []string{"QmTest", "QmLow", "QmHigh", "QmSeg0"},
	}

	if result.MasterCID != "QmTest" {
		t.Errorf("MasterCID = %q, want QmTest", result.MasterCID)
	}
	if len(result.VariantCIDs) != 2 {
		t.Errorf("VariantCIDs len = %d, want 2", len(result.VariantCIDs))
	}
	if len(result.AllCIDs) != 4 {
		t.Errorf("AllCIDs len = %d, want 4", len(result.AllCIDs))
	}
}

func TestVideoResponseJSON(t *testing.T) {
	resp := VideoResponse{
		MasterCID:   "QmTest",
		VariantCIDs: map[string]string{"low": "QmLow"},
		DurationSec: 45.5,
		Status:      "processing_done",
	}

	if resp.MasterCID != "QmTest" {
		t.Error("VideoResponse MasterCID mismatch")
	}
	if resp.DurationSec != 45.5 {
		t.Error("VideoResponse DurationSec mismatch")
	}
	if resp.Status != "processing_done" {
		t.Error("VideoResponse Status mismatch")
	}
}

func TestBuildMasterPlaylist(t *testing.T) {
	u := NewUploader(nil)
	variantCIDs := map[string]string{
		"low":    "QmLowCID",
		"medium": "QmMedCID",
		"high":   "QmHighCID",
	}

	content, err := u.buildMasterPlaylist(variantCIDs, nil)
	if err != nil {
		t.Fatalf("buildMasterPlaylist failed: %v", err)
	}

	if !contains(content, "#EXTM3U") {
		t.Error("Master playlist missing #EXTM3U header")
	}
	if !contains(content, "QmLowCID") {
		t.Error("Master playlist missing low variant CID")
	}
	if !contains(content, "QmMedCID") {
		t.Error("Master playlist missing medium variant CID")
	}
	if !contains(content, "QmHighCID") {
		t.Error("Master playlist missing high variant CID")
	}
	if !contains(content, "BANDWIDTH=500000") {
		t.Error("Master playlist missing low bandwidth")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestVideoInfo(t *testing.T) {
	info := &VideoInfo{
		Duration:  30.5,
		Width:     1080,
		Height:    1920,
		CodecName: "h264",
	}

	if info.Duration != 30.5 {
		t.Errorf("VideoInfo Duration = %f, want 30.5", info.Duration)
	}
	if info.Width != 1080 || info.Height != 1920 {
		t.Errorf("VideoInfo dimensions = %dx%d, want 1080x1920", info.Width, info.Height)
	}

	// Проверяем пропорцию 9:16
	ratio := float64(info.Width) / float64(info.Height)
	target := 9.0 / 16.0
	diff := ratio - target
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.1 {
		t.Errorf("Aspect ratio %.3f not within tolerance of 9:16 (%.3f)", ratio, target)
	}
}

func TestVideoConfigDefaults(t *testing.T) {
	// Тестируем что VideoConfig имеет разумные defaults
	cfg := &VideoConfig{
		MaxDurationSec:       60,
		MaxSizeBytes:         30 * 1024 * 1024,
		AspectRatioTolerance: 0.1,
		SegmentDurationSec:   4,
		Bitrates:             []string{"500k", "1500k", "4000k"},
		FFmpegPath:           "ffmpeg",
		FFprobePath:          "ffprobe",
		TempDir:              "/tmp/video_processing",
	}

	if cfg.MaxDurationSec != 60 {
		t.Errorf("Default MaxDurationSec = %d, want 60", cfg.MaxDurationSec)
	}
	if cfg.SegmentDurationSec != 4 {
		t.Errorf("Default SegmentDurationSec = %d, want 4", cfg.SegmentDurationSec)
	}
	if len(cfg.Bitrates) != 3 {
		t.Errorf("Default Bitrates len = %d, want 3", len(cfg.Bitrates))
	}
}

func TestProbeDurationInvalidPath(t *testing.T) {
	cfg := &VideoConfig{
		FFprobePath: "ffprobe",
	}

	tr := NewTranscoder(cfg)
	ctx := t.Context()

	_, err := tr.probeDuration(ctx, "/nonexistent/file.mp4")
	if err == nil {
		t.Error("Expected error for nonexistent file in probeDuration")
	}
}

func TestRewriteVariantPlaylist(t *testing.T) {
	// Создаём временную директорию с вариантным плейлистом
	dir := t.TempDir()
	lowDir := filepath.Join(dir, "low")
	os.MkdirAll(lowDir, 0o755)

	playlistContent := "#EXTM3U\n#EXT-X-VERSION:6\n#EXTINF:4.000,\nseg_0.m4s\n#EXTINF:4.000,\nseg_1.m4s\n#EXT-X-ENDLIST\n"
	err := os.WriteFile(filepath.Join(lowDir, "playlist.m3u8"), []byte(playlistContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	fileCIDs := map[string]string{
		"low/seg_0.m4s": "QmSeg0",
		"low/seg_1.m4s": "QmSeg1",
	}

	u := NewUploader(nil)
	result, err := u.rewriteVariantPlaylist(dir, "low/playlist.m3u8", fileCIDs)
	if err != nil {
		t.Fatalf("rewriteVariantPlaylist failed: %v", err)
	}

	if !containsStr(result, "QmSeg0.m4s") {
		t.Errorf("Rewritten playlist should contain CID for seg_0, got: %s", result)
	}
	if !containsStr(result, "QmSeg1.m4s") {
		t.Errorf("Rewritten playlist should contain CID for seg_1, got: %s", result)
	}
	if containsStr(result, "seg_0.m4s\n") {
		t.Error("Rewritten playlist should NOT contain original segment filename")
	}
}
