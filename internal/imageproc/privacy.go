package imageproc

import (
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"gocv.io/x/gocv"
	xdraw "golang.org/x/image/draw"
)

//go:embed assets/face_detection_yunet_2023mar.onnx
var yuNetModel []byte

type faceDetector interface {
	Detect(image.Image) ([]image.Rectangle, error)
}

type yuNetFaceDetector struct {
	cfg      config.ImagePrivacyConfig
	detector gocv.FaceDetectorYN
	mu       sync.Mutex
}

func newYuNetFaceDetector(cfg config.ImagePrivacyConfig) (*yuNetFaceDetector, error) {
	if len(yuNetModel) == 0 {
		return nil, fmt.Errorf("embedded YuNet model is empty")
	}

	gocv.ClearLastException()
	detector := gocv.NewFaceDetectorYNFromBytesWithParams(
		"onnx",
		yuNetModel,
		nil,
		image.Pt(32, 32),
		float32(cfg.FaceDetectionScoreThreshold),
		float32(cfg.FaceDetectionNMSThreshold),
		5000,
		0,
		0,
	)
	if err := gocv.LastExceptionError(); err != nil {
		return nil, fmt.Errorf("initialize embedded YuNet detector: %w", err)
	}
	return &yuNetFaceDetector{cfg: cfg, detector: detector}, nil
}

func (d *yuNetFaceDetector) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.detector.Close()
}

func (d *yuNetFaceDetector) Detect(src image.Image) ([]image.Rectangle, error) {
	if d == nil {
		return nil, fmt.Errorf("face detector is not initialized")
	}

	detectionImage, scale := scaleForFaceDetection(src, d.cfg.FaceDetectionMaxDimension)
	inputWidth := roundUpToMultiple(detectionImage.Bounds().Dx(), 32)
	inputHeight := roundUpToMultiple(detectionImage.Bounds().Dy(), 32)
	input, err := newBGRMat(detectionImage, inputWidth, inputHeight)
	if err != nil {
		return nil, fmt.Errorf("prepare YuNet input: %w", err)
	}
	defer input.Close()

	detections := gocv.NewMat()
	defer detections.Close()

	d.mu.Lock()
	gocv.ClearLastException()
	d.detector.SetInputSize(image.Pt(inputWidth, inputHeight))
	ok := d.detector.Detect(input, &detections)
	err = gocv.LastExceptionError()
	d.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("run YuNet detector: %w", err)
	}
	if ok == 0 || detections.Empty() {
		return nil, nil
	}
	if detections.Cols() < 15 {
		return nil, fmt.Errorf("unexpected YuNet detection matrix: %d columns", detections.Cols())
	}

	faces := make([]image.Rectangle, 0, detections.Rows())
	originalBounds := src.Bounds()
	for row := 0; row < detections.Rows(); row++ {
		rect := image.Rect(
			int(math.Round(float64(detections.GetFloatAt(row, 0))/scale)),
			int(math.Round(float64(detections.GetFloatAt(row, 1))/scale)),
			int(math.Round(float64(detections.GetFloatAt(row, 0)+detections.GetFloatAt(row, 2))/scale)),
			int(math.Round(float64(detections.GetFloatAt(row, 1)+detections.GetFloatAt(row, 3))/scale)),
		)
		rect = expandFaceRect(rect, originalBounds)
		if !rect.Empty() {
			faces = append(faces, rect)
		}
	}
	return faces, nil
}

func newBGRMat(src *image.NRGBA, width, height int) (gocv.Mat, error) {
	data := make([]byte, width*height*3)
	bounds := src.Bounds()
	for y := 0; y < bounds.Dy(); y++ {
		sourceOffset := y * src.Stride
		targetOffset := y * width * 3
		for x := 0; x < bounds.Dx(); x++ {
			pixelOffset := sourceOffset + x*4
			dataOffset := targetOffset + x*3
			data[dataOffset] = src.Pix[pixelOffset+2]
			data[dataOffset+1] = src.Pix[pixelOffset+1]
			data[dataOffset+2] = src.Pix[pixelOffset]
		}
	}
	return gocv.NewMatFromBytes(height, width, gocv.MatTypeCV8UC3, data)
}

func roundUpToMultiple(value, multiple int) int {
	if value <= 0 || multiple <= 0 {
		return 0
	}
	return ((value + multiple - 1) / multiple) * multiple
}

func scaleForFaceDetection(src image.Image, maxDimension int) (*image.NRGBA, float64) {
	normalized := toNRGBA(src)
	bounds := normalized.Bounds()
	longest := max(bounds.Dx(), bounds.Dy())
	if maxDimension <= 0 || longest <= maxDimension {
		return normalized, 1
	}
	scale := float64(maxDimension) / float64(longest)
	width := max(1, int(math.Round(float64(bounds.Dx())*scale)))
	height := max(1, int(math.Round(float64(bounds.Dy())*scale)))
	resized := image.NewNRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(resized, resized.Bounds(), normalized, bounds, xdraw.Src, nil)
	return resized, scale
}

func expandFaceRect(rect, bounds image.Rectangle) image.Rectangle {
	padding := max(2, min(rect.Dx(), rect.Dy())/4)
	return image.Rect(rect.Min.X-padding, rect.Min.Y-padding, rect.Max.X+padding, rect.Max.Y+padding).Intersect(bounds)
}

func toNRGBA(src image.Image) *image.NRGBA {
	if normalized, ok := src.(*image.NRGBA); ok && normalized.Bounds().Min == (image.Point{}) {
		return normalized
	}
	bounds := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Src)
	return dst
}

func cloneNRGBA(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

// privacyVariants creates two stable variants. blur is a complete image blur;
// blur_faces only masks the areas found by the embedded local detector. If the
// detector finds no face, blur_faces intentionally falls back to the complete
// blur so it never becomes an unprotected copy of the source image.
func (p *Processor) privacyVariants(ctx context.Context, src image.Image, outputFormat string) ([]Variant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.faceDetectorErr != nil {
		return nil, p.faceDetectorErr
	}
	if p.faceDetector == nil {
		return nil, fmt.Errorf("face detector is not initialized")
	}

	normalized := toNRGBA(src)
	wholeBlur := blurNRGBA(normalized, p.cfg.Privacy.BlurRadius)
	wholeEncoded, err := p.encode(ctx, wholeBlur, outputFormat)
	if err != nil {
		return nil, fmt.Errorf("encode image privacy variant %s: %w", config.PrivacyBlurVariantKey, err)
	}

	faces, err := p.faceDetector.Detect(normalized)
	if err != nil {
		return nil, fmt.Errorf("detect faces: %w", err)
	}
	facesBlur := wholeBlur
	if len(faces) > 0 {
		facesBlur = blurFaceRegions(normalized, faces, p.cfg.Privacy.FaceBlurRadius)
	}
	facesEncoded, err := p.encode(ctx, facesBlur, outputFormat)
	if err != nil {
		return nil, fmt.Errorf("encode image privacy variant %s: %w", config.PrivacyFaceBlurVariantKey, err)
	}

	width := normalized.Bounds().Dx()
	height := normalized.Bounds().Dy()
	return []Variant{
		{
			Key:         config.PrivacyBlurVariantKey,
			Filename:    config.PrivacyBlurVariantKey + "." + extension(outputFormat),
			Data:        wholeEncoded,
			Format:      outputFormat,
			ContentType: contentTypeForFormat(outputFormat),
			Width:       width,
			Height:      height,
		},
		{
			Key:         config.PrivacyFaceBlurVariantKey,
			Filename:    config.PrivacyFaceBlurVariantKey + "." + extension(outputFormat),
			Data:        facesEncoded,
			Format:      outputFormat,
			ContentType: contentTypeForFormat(outputFormat),
			Width:       width,
			Height:      height,
		},
	}, nil
}

func blurFaceRegions(src *image.NRGBA, faces []image.Rectangle, minimumRadius int) *image.NRGBA {
	dst := cloneNRGBA(src)
	for _, face := range faces {
		face = face.Intersect(src.Bounds())
		if face.Empty() {
			continue
		}
		radius := max(minimumRadius, min(face.Dx(), face.Dy())/5)
		region := image.NewNRGBA(image.Rect(0, 0, face.Dx(), face.Dy()))
		draw.Draw(region, region.Bounds(), dst, face.Min, draw.Src)
		blurred := blurNRGBA(region, radius)
		applyEllipse(dst, blurred, face)
	}
	return dst
}

func applyEllipse(dst, blurred *image.NRGBA, face image.Rectangle) {
	centerX := float64(face.Min.X+face.Max.X-1) / 2
	centerY := float64(face.Min.Y+face.Max.Y-1) / 2
	radiusX := max(1.0, float64(face.Dx())/2)
	radiusY := max(1.0, float64(face.Dy())/2)
	for y := face.Min.Y; y < face.Max.Y; y++ {
		for x := face.Min.X; x < face.Max.X; x++ {
			dx := (float64(x) - centerX) / radiusX
			dy := (float64(y) - centerY) / radiusY
			if dx*dx+dy*dy > 1 {
				continue
			}
			dst.SetNRGBA(x, y, blurred.NRGBAAt(x-face.Min.X, y-face.Min.Y))
		}
	}
}

// blurNRGBA uses three box-blur passes to approximate a Gaussian blur in
// linear time. The implementation keeps transparent image colors premultiplied
// while averaging, so PNG/WebP transparency does not create dark halos.
func blurNRGBA(src *image.NRGBA, radius int) *image.NRGBA {
	if radius <= 0 {
		return cloneNRGBA(src)
	}
	result := cloneNRGBA(src)
	for i := 0; i < 3; i++ {
		result = boxBlurHorizontal(result, radius)
		result = boxBlurVertical(result, radius)
	}
	return result
}

func boxBlurHorizontal(src *image.NRGBA, radius int) *image.NRGBA {
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		var red, green, blue, alpha int
		count := 0
		for x := bounds.Min.X; x <= min(bounds.Max.X-1, bounds.Min.X+radius); x++ {
			r, g, b, a := premultiplied(src.NRGBAAt(x, y))
			red += r
			green += g
			blue += b
			alpha += a
			count++
		}
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.SetNRGBA(x, y, unpremultiplied(red, green, blue, alpha, count))
			removeX := x - radius
			if removeX >= bounds.Min.X {
				r, g, b, a := premultiplied(src.NRGBAAt(removeX, y))
				red -= r
				green -= g
				blue -= b
				alpha -= a
				count--
			}
			addX := x + radius + 1
			if addX < bounds.Max.X {
				r, g, b, a := premultiplied(src.NRGBAAt(addX, y))
				red += r
				green += g
				blue += b
				alpha += a
				count++
			}
		}
	}
	return dst
}

func boxBlurVertical(src *image.NRGBA, radius int) *image.NRGBA {
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		var red, green, blue, alpha int
		count := 0
		for y := bounds.Min.Y; y <= min(bounds.Max.Y-1, bounds.Min.Y+radius); y++ {
			r, g, b, a := premultiplied(src.NRGBAAt(x, y))
			red += r
			green += g
			blue += b
			alpha += a
			count++
		}
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			dst.SetNRGBA(x, y, unpremultiplied(red, green, blue, alpha, count))
			removeY := y - radius
			if removeY >= bounds.Min.Y {
				r, g, b, a := premultiplied(src.NRGBAAt(x, removeY))
				red -= r
				green -= g
				blue -= b
				alpha -= a
				count--
			}
			addY := y + radius + 1
			if addY < bounds.Max.Y {
				r, g, b, a := premultiplied(src.NRGBAAt(x, addY))
				red += r
				green += g
				blue += b
				alpha += a
				count++
			}
		}
	}
	return dst
}

func premultiplied(c color.NRGBA) (int, int, int, int) {
	alpha := int(c.A)
	return int(c.R) * alpha, int(c.G) * alpha, int(c.B) * alpha, alpha
}

func unpremultiplied(red, green, blue, alpha, count int) color.NRGBA {
	if count == 0 || alpha == 0 {
		return color.NRGBA{}
	}
	averageAlpha := alpha / count
	return color.NRGBA{
		R: uint8(min(255, red/alpha)),
		G: uint8(min(255, green/alpha)),
		B: uint8(min(255, blue/alpha)),
		A: uint8(averageAlpha),
	}
}
