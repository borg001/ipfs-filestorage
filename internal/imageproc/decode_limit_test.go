package imageproc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

// A small file declaring enormous dimensions is refused before it is decoded.
func TestProcessRefusesADecompressionBomb(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	// IHDR follows the 8-byte signature: length(4) "IHDR"(4) width(4) height(4) ...
	binary.BigEndian.PutUint32(data[16:20], 20000)
	binary.BigEndian.PutUint32(data[20:24], 20000)
	crc := crc32.ChecksumIEEE(data[12:29])
	binary.BigEndian.PutUint32(data[29:33], crc)

	processor := NewProcessor(config.ImageConfig{}, "")
	if _, err := processor.Process(context.Background(), data, "image/png"); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("err = %v, want ErrImageTooLarge", err)
	}
}
