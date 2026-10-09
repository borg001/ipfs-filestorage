package imageproc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// heifConvertPath is the libheif command line tool. It is used the same way
// jpegtran and ffmpeg are: the format work stays in the tool that owns it.
const heifConvertPath = "heif-convert"

// heifBrands are the ISOBMFF brands of a still image an ordinary browser
// cannot display. A phone shooting in "High Efficiency" hands over exactly
// these.
var heifBrands = map[string]bool{
	"heic": true, "heix": true, "heim": true, "heis": true,
	"hevc": true, "hevx": true, "hevm": true, "hevs": true,
	"mif1": true, "msf1": true,
}

// IsHEIF reports whether the payload is a HEIF/HEIC still image. The answer is
// read from the file itself: a browser reports the type inconsistently and Go's
// own content sniffing does not know the format at all.
func IsHEIF(data []byte) bool {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return false
	}
	return heifBrands[strings.ToLower(string(data[8:12]))]
}

// JPEGFilename renames a converted source so its stored name says what it now
// is.
func JPEGFilename(filename string) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if strings.TrimSpace(base) == "" {
		base = "image"
	}
	return base + ".jpg"
}

// TranscodeHEIF turns a HEIF/HEIC still into a JPEG. It runs once, at ingest,
// so the stored original, every variant and the delivery link all carry a
// format every browser can display.
func (p *Processor) TranscodeHEIF(ctx context.Context, data []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "heif")
	if err != nil {
		return nil, fmt.Errorf("heif workspace: %w", err)
	}
	defer os.RemoveAll(dir)

	source := filepath.Join(dir, "source.heic")
	target := filepath.Join(dir, "source.jpg")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		return nil, fmt.Errorf("heif source: %w", err)
	}

	quality := p.cfg.JPEGQuality
	if quality < 1 || quality > 100 {
		quality = 90
	}
	cmd := exec.CommandContext(ctx, heifConvertPath, "-q", strconv.Itoa(quality), source, target)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("heif-convert failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	converted, err := os.ReadFile(target)
	if err != nil || len(converted) == 0 {
		// A file holding several images is written as source-1.jpg and so on.
		// The first image of such a source is the photo that was uploaded.
		matches, globErr := filepath.Glob(filepath.Join(dir, "source-*.jpg"))
		if globErr != nil || len(matches) == 0 {
			return nil, fmt.Errorf("heif-convert produced no image")
		}
		converted, err = os.ReadFile(matches[0])
		if err != nil || len(converted) == 0 {
			return nil, fmt.Errorf("heif-convert produced no image")
		}
	}
	return converted, nil
}
