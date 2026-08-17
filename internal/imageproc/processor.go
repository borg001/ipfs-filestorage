package imageproc

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os/exec"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/config"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Variant struct {
	Key         string
	Filename    string
	Data        []byte
	Format      string
	ContentType string
	Width       int
	Height      int
}

type Result struct {
	IsImage  bool
	Format   string
	Width    int
	Height   int
	Variants []Variant
}

type Processor struct {
	cfg        config.ImageConfig
	ffmpegPath string
}

func NewProcessor(cfg config.ImageConfig, ffmpegPath string) *Processor {
	return &Processor{cfg: cfg, ffmpegPath: ffmpegPath}
}

func (p *Processor) Process(ctx context.Context, data []byte, contentType string) (Result, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{IsImage: false}, nil
	}

	result := Result{
		IsImage: true,
		Format:  normalizeFormat(format, contentType),
		Width:   cfg.Width,
		Height:  cfg.Height,
	}
	if !p.cfg.ProcessingEnabled || len(p.cfg.Variants) == 0 {
		return result, nil
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return result, nil
	}

	for _, variantCfg := range p.cfg.Variants {
		if variantCfg.Width <= 0 || variantCfg.Height <= 0 {
			continue
		}
		outputFormat := p.outputFormat(result.Format)
		resized := resize(src, variantCfg.Width, variantCfg.Height, p.cfg.ResizePolicy, outputFormat == "jpeg")
		encoded, err := p.encode(ctx, resized, outputFormat)
		if err != nil {
			return result, fmt.Errorf("encode image variant %s: %w", variantCfg.Key, err)
		}
		result.Variants = append(result.Variants, Variant{
			Key:         variantCfg.Key,
			Filename:    variantCfg.Key + "." + extension(outputFormat),
			Data:        encoded,
			Format:      outputFormat,
			ContentType: contentTypeForFormat(outputFormat),
			Width:       variantCfg.Width,
			Height:      variantCfg.Height,
		})
	}

	return result, nil
}

// PrivacyBlur produces a separate, physically blurred image. It is uploaded as
// an independent IPFS bundle so a consumer that receives its CID cannot derive
// or request the original image by changing the URL path.
func (p *Processor) PrivacyBlur(ctx context.Context, data []byte, contentType string) (Variant, error) {
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Variant{}, err
	}

	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return Variant{}, fmt.Errorf("invalid image dimensions")
	}
	const maxDimension = 1600
	if width > maxDimension || height > maxDimension {
		scale := math.Min(float64(maxDimension)/float64(width), float64(maxDimension)/float64(height))
		width = max(1, int(math.Round(float64(width)*scale)))
		height = max(1, int(math.Round(float64(height)*scale)))
	}

	// Downscale heavily and restore with a smooth interpolator. The resulting
	// bytes contain the blur itself; no CSS filter or placeholder is involved.
	blurWidth := max(12, width/28)
	blurHeight := max(12, height/28)
	low := image.NewNRGBA(image.Rect(0, 0, blurWidth, blurHeight))
	xdraw.ApproxBiLinear.Scale(low, low.Bounds(), src, bounds, xdraw.Src, nil)
	blurred := image.NewNRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(blurred, blurred.Bounds(), low, low.Bounds(), xdraw.Src, nil)

	normalized := normalizeFormat(format, contentType)
	outputFormat := p.outputFormat(normalized)
	encoded, err := p.encode(ctx, blurred, outputFormat)
	if err != nil {
		return Variant{}, fmt.Errorf("encode privacy blur: %w", err)
	}
	return Variant{
		Key:         "privacy-blur",
		Filename:    "privacy-blur." + extension(outputFormat),
		Data:        encoded,
		Format:      outputFormat,
		ContentType: contentTypeForFormat(outputFormat),
		Width:       width,
		Height:      height,
	}, nil
}

func (p *Processor) outputFormat(sourceFormat string) string {
	switch p.cfg.OutputFormat {
	case "jpeg", "webp":
		return p.cfg.OutputFormat
	default:
		if sourceFormat == "png" || sourceFormat == "webp" {
			return "webp"
		}
		return "jpeg"
	}
}

func (p *Processor) encode(ctx context.Context, img image.Image, format string) ([]byte, error) {
	if format == "webp" {
		return p.encodeWithFFmpeg(ctx, img, "webp")
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: p.cfg.JPEGQuality}); err != nil {
		return nil, err
	}
	if p.cfg.JPEGProgressive {
		return encodeProgressiveJPEG(ctx, buf.Bytes())
	}
	return buf.Bytes(), nil
}

func encodeProgressiveJPEG(ctx context.Context, baseline []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "jpegtran", "-progressive", "-copy", "none", "-optimize")
	cmd.Stdin = bytes.NewReader(baseline)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("jpegtran failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

func (p *Processor) encodeWithFFmpeg(ctx context.Context, img image.Image, format string) ([]byte, error) {
	if p.ffmpegPath == "" {
		p.ffmpegPath = "ffmpeg"
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, err
	}

	args := []string{"-v", "error", "-f", "png_pipe", "-i", "pipe:0", "-frames:v", "1"}
	switch format {
	case "webp":
		args = append(args, "-f", "image2pipe", "-vcodec", "libwebp", "-quality", fmt.Sprintf("%d", p.cfg.WebPQuality), "pipe:1")
	default:
		return nil, fmt.Errorf("unsupported output format %q", format)
	}

	cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
	cmd.Stdin = bytes.NewReader(pngBuf.Bytes())
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

func resize(src image.Image, width, height int, policy string, whiteBackground bool) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	if whiteBackground {
		draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	}

	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW == 0 || srcH == 0 {
		return dst
	}

	if policy == "fit" {
		scale := math.Min(float64(width)/float64(srcW), float64(height)/float64(srcH))
		newW := max(1, int(math.Round(float64(srcW)*scale)))
		newH := max(1, int(math.Round(float64(srcH)*scale)))
		rect := image.Rect((width-newW)/2, (height-newH)/2, (width-newW)/2+newW, (height-newH)/2+newH)
		xdraw.CatmullRom.Scale(dst, rect, src, srcBounds, xdraw.Over, nil)
		return dst
	}

	scale := math.Max(float64(width)/float64(srcW), float64(height)/float64(srcH))
	newW := max(1, int(math.Round(float64(srcW)*scale)))
	newH := max(1, int(math.Round(float64(srcH)*scale)))
	scaled := image.NewNRGBA(image.Rect(0, 0, newW, newH))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, srcBounds, xdraw.Over, nil)

	offsetX := (newW - width) / 2
	offsetY := (newH - height) / 2
	draw.Draw(dst, dst.Bounds(), scaled, image.Point{X: offsetX, Y: offsetY}, draw.Over)
	return dst
}

func normalizeFormat(format, contentType string) string {
	if format == "jpeg" || format == "png" || format == "webp" {
		return format
	}
	if strings.Contains(contentType, "jpeg") {
		return "jpeg"
	}
	if strings.Contains(contentType, "png") {
		return "png"
	}
	if strings.Contains(contentType, "webp") {
		return "webp"
	}
	return format
}

func contentTypeForFormat(format string) string {
	if format == "webp" {
		return "image/webp"
	}
	return "image/jpeg"
}

func extension(format string) string {
	if format == "jpeg" {
		return "jpg"
	}
	return format
}
