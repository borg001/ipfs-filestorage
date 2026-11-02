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

// An avatar framed in the gallery is cut from the part it was framed to:
// the right half of a picture red on the left and blue on the right comes
// out blue (theGHub1/api#591).
func TestCroppedPreviewCutsTheFramedPart(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			if x < 100 {
				source.Set(x, y, color.NRGBA{R: 255, A: 255})
			} else {
				source.Set(x, y, color.NRGBA{B: 255, A: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	p := NewProcessor(config.ImageConfig{JPEGQuality: 90}, "")
	preview, err := p.CroppedPreview(context.Background(), encoded.Bytes(), config.ImageVariant{Width: 40, Height: 40, Mode: "fit"}, 0.5, 0, 0.5, 1)
	if err != nil {
		t.Fatal(err)
	}
	framed, err := jpeg.Decode(bytes.NewReader(preview))
	if err != nil {
		t.Fatal(err)
	}
	if framed.Bounds().Dx() != 40 || framed.Bounds().Dy() != 40 {
		t.Fatalf("the framed part is %v, want the square it was cut to", framed.Bounds())
	}
	r, _, b, _ := framed.At(20, 20).RGBA()
	if b < 0xC000 || r > 0x4000 {
		t.Fatalf("the framed part is not the right half: r=%x b=%x", r, b)
	}
	if _, err := p.CroppedPreview(context.Background(), encoded.Bytes(), config.ImageVariant{Width: 40, Height: 40, Mode: "fit"}, 1, 1, 0, 0); err == nil {
		t.Fatal("a frame outside the picture was cut")
	}
}
