package video

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A browser recording states no duration: it is taken from its last frame.
func TestLastFrameEnd(t *testing.T) {
	end, err := lastFrameEnd("0.000000,0.033000,\n2.900000,0.033000,\nN/A,N/A\n1.000000,0.033000\n")
	if err != nil || end < 2.932 || end > 2.934 {
		t.Fatalf("end = %v, %v; want 2.933", end, err)
	}
	if _, err := lastFrameEnd("N/A,N/A\n"); err == nil {
		t.Fatal("a video without timed frames has no duration")
	}
}

// A WebM written without seeking back (as MediaRecorder writes it) has no
// duration in its header and is still measured and accepted.
func TestProbeMeasuresAWebmWithoutDuration(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}
	path := filepath.Join(t.TempDir(), "recorded.webm")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=d=2:s=64x48:r=10", "-c:v", "libvpx", "-f", "webm", "pipe:1")
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		t.Skipf("ffmpeg cannot write vp8 webm: %v", err)
	}
	out.Close()
	info, err := probeVideoInfo(context.Background(), ffprobe, path)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if info.Duration < 1.8 || info.Duration > 2.2 || info.Width != 64 {
		t.Fatalf("info = %+v; want about 2 s of 64 px", info)
	}
}
