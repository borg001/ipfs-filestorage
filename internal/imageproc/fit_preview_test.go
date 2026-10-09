package imageproc

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestFitPreviewPreservesWholeLandscapeAndBoundsSize(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1600, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 1600; x++ {
			if x < 160 {
				src.Set(x, y, color.RGBA{R: 255, A: 255})
			} else if x >= 1440 {
				src.Set(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	var input bytes.Buffer
	if err := jpeg.Encode(&input, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	p := NewProcessor(config.ImageConfig{JPEGQuality: 82}, "")
	preview, err := p.FitPreview(context.Background(), input.Bytes(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(preview))
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds().Dx() != 1024 || got.Bounds().Dy() != 512 {
		t.Fatalf("preview dimensions = %v", got.Bounds())
	}
	if got.At(5, 256).(color.YCbCr).Y < 50 || got.At(1018, 256).(color.YCbCr).Cb < 100 {
		t.Fatal("preview lost an edge of the original")
	}
}
