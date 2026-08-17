package imageproc

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

type staticFaceDetector struct {
	faces []image.Rectangle
	err   error
}

func (d staticFaceDetector) Detect(image.Image) ([]image.Rectangle, error) {
	return d.faces, d.err
}

func TestPigoFaceDetectorDetectsFace(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "pigo-sample.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	detector, err := newPigoFaceDetector(config.DefaultImagePrivacyConfig())
	if err != nil {
		t.Fatal(err)
	}
	faces, err := detector.Detect(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(faces) == 0 {
		t.Fatal("expected the embedded detector to find a face in the reference image")
	}
}

func TestProcessorGeneratesPrivacyVariantsEvenWithoutResizes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "pigo-sample.jpg"))
	if err != nil {
		t.Fatal(err)
	}

	processor := NewProcessor(config.ImageConfig{
		ProcessingEnabled: false,
		OutputFormat:      "jpeg",
		JPEGQuality:       82,
		Privacy:           config.DefaultImagePrivacyConfig(),
	}, "")
	result, err := processor.Process(context.Background(), data, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsImage || result.Width != 320 || result.Height != 400 {
		t.Fatalf("unexpected source result: %+v", result)
	}
	variants := variantsByKey(result.Variants)
	for _, key := range []string{config.PrivacyBlurVariantKey, config.PrivacyFaceBlurVariantKey} {
		variant, ok := variants[key]
		if !ok {
			t.Fatalf("privacy variant %q missing: %+v", key, result.Variants)
		}
		if variant.Format != "jpeg" || variant.ContentType != "image/jpeg" || variant.Width != 320 || variant.Height != 400 {
			t.Fatalf("unexpected variant %q: %+v", key, variant)
		}
		if len(variant.Data) == 0 || bytes.Equal(variant.Data, data) {
			t.Fatalf("privacy variant %q was not encoded as a distinct image", key)
		}
	}
}

func TestProcessorBlursDetectedFaceInReferencePhoto(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "pigo-sample.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	processor := NewProcessor(config.ImageConfig{
		OutputFormat: "jpeg",
		JPEGQuality:  82,
		Privacy:      config.DefaultImagePrivacyConfig(),
	}, "")
	faces, err := processor.faceDetector.Detect(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(faces) == 0 {
		t.Fatal("reference photo has no detected face")
	}

	result, err := processor.Process(context.Background(), data, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	faceVariant := variantsByKey(result.Variants)[config.PrivacyFaceBlurVariantKey]
	faceBlurred, err := jpeg.Decode(bytes.NewReader(faceVariant.Data))
	if err != nil {
		t.Fatal(err)
	}
	baselineData, err := processor.encode(context.Background(), src, "jpeg")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := jpeg.Decode(bytes.NewReader(baselineData))
	if err != nil {
		t.Fatal(err)
	}

	difference := meanRGBDifferenceInEllipse(baseline, faceBlurred, faces[0])
	if difference < 12 {
		t.Fatalf("detected face was not sufficiently blurred: mean RGB difference %.2f", difference)
	}
}

func TestProcessorUsesWholeBlurWhenNoFaceIsDetected(t *testing.T) {
	data := encodeTestImage(t)
	processor := NewProcessor(config.ImageConfig{
		OutputFormat: "jpeg",
		JPEGQuality:  82,
		Privacy:      config.DefaultImagePrivacyConfig(),
	}, "")
	processor.faceDetector = staticFaceDetector{}
	result, err := processor.Process(context.Background(), data, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	variants := variantsByKey(result.Variants)
	if !bytes.Equal(variants[config.PrivacyBlurVariantKey].Data, variants[config.PrivacyFaceBlurVariantKey].Data) {
		t.Fatal("face privacy variant must fall back to whole-image blur when no face is detected")
	}
}

func TestBlurFaceRegionsOnlyChangesFaceEllipse(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 80, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 80; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8((x * 17) % 255), G: uint8((y * 29) % 255), B: 160, A: 255})
		}
	}
	blurred := blurFaceRegions(src, []image.Rectangle{image.Rect(20, 20, 60, 60)}, 8)
	if blurred.NRGBAAt(40, 40) == src.NRGBAAt(40, 40) {
		t.Fatal("face center was not blurred")
	}
	if blurred.NRGBAAt(0, 0) != src.NRGBAAt(0, 0) {
		t.Fatal("pixels outside the face ellipse must remain unchanged")
	}
}

func variantsByKey(variants []Variant) map[string]Variant {
	result := make(map[string]Variant, len(variants))
	for _, variant := range variants {
		result[variant.Key] = variant
	}
	return result
}

func encodeTestImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 96, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 96; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 2), G: uint8(y * 2), B: 180, A: 255})
		}
	}
	var data bytes.Buffer
	if err := jpeg.Encode(&data, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func meanRGBDifferenceInEllipse(left, right image.Image, rect image.Rectangle) float64 {
	centerX := float64(rect.Min.X+rect.Max.X-1) / 2
	centerY := float64(rect.Min.Y+rect.Max.Y-1) / 2
	radiusX := max(1.0, float64(rect.Dx())/2)
	radiusY := max(1.0, float64(rect.Dy())/2)
	var total float64
	count := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			dx := (float64(x) - centerX) / radiusX
			dy := (float64(y) - centerY) / radiusY
			if dx*dx+dy*dy > 1 {
				continue
			}
			lr, lg, lb, _ := left.At(x, y).RGBA()
			rr, rg, rb, _ := right.At(x, y).RGBA()
			total += math.Abs(float64(lr>>8) - float64(rr>>8))
			total += math.Abs(float64(lg>>8) - float64(rg>>8))
			total += math.Abs(float64(lb>>8) - float64(rb>>8))
			count += 3
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}
