package imageproc

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func exifWithGPSAndOrientation(orientation uint16) []byte {
	// Little-endian TIFF: IFD0 with Orientation and a GPS IFD pointer, and a
	// GPS IFD with a latitude reference - enough for "GPS" to be findable.
	tiff := []byte{'I', 'I', 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00}
	ifd := make([]byte, 2+2*12+4)
	binary.LittleEndian.PutUint16(ifd[0:2], 2)
	// Orientation
	binary.LittleEndian.PutUint16(ifd[2:4], 0x0112)
	binary.LittleEndian.PutUint16(ifd[4:6], 3)
	binary.LittleEndian.PutUint32(ifd[6:10], 1)
	binary.LittleEndian.PutUint16(ifd[10:12], orientation)
	// GPS IFD pointer
	binary.LittleEndian.PutUint16(ifd[14:16], 0x8825)
	binary.LittleEndian.PutUint16(ifd[16:18], 4)
	binary.LittleEndian.PutUint32(ifd[18:22], 1)
	binary.LittleEndian.PutUint32(ifd[22:26], uint32(8+len(ifd)))
	gps := []byte{0x01, 0x00, 0x01, 0x00, 0x02, 0x00, 0x02, 0x00, 0x00, 0x00, 'N', 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	payload := append([]byte("Exif\x00\x00"), append(append(tiff, ifd...), append([]byte("GPSLatitude55.75"), gps...)...)...)
	segment := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	return append(segment, payload...)
}

func TestStripMetadataDropsEXIFAndKeepsOrientation(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 8, 4)), nil); err != nil {
		t.Fatal(err)
	}
	plain := encoded.Bytes()
	tagged := append(append([]byte{0xFF, 0xD8}, exifWithGPSAndOrientation(6)...), plain[2:]...)
	tagged = append(tagged[:0:0], tagged...)
	if !bytes.Contains(tagged, []byte("GPSLatitude")) {
		t.Fatal("fixture has no GPS")
	}

	stripped := StripMetadata(tagged)
	if bytes.Contains(stripped, []byte("GPSLatitude")) {
		t.Fatal("GPS survived")
	}
	if got := jpegOrientation(stripped); got != 6 {
		t.Fatalf("orientation = %d, want 6", got)
	}
	if _, err := jpeg.Decode(bytes.NewReader(stripped)); err != nil {
		t.Fatalf("stripped JPEG does not decode: %v", err)
	}
	// A picture with nothing to strip keeps its bytes' meaning.
	if again := StripMetadata(plain); !bytes.Equal(again, plain) {
		t.Fatal("a plain JPEG changed")
	}
}

func TestStripMetadataDropsPNGText(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	// Insert a text chunk after IHDR (8 signature + 25 IHDR bytes).
	tagged := append(append(append([]byte{}, data[:33]...), pngChunk("tEXt", []byte("Location\x0055.75,37.61"))...), data[33:]...)
	stripped := StripMetadata(tagged)
	if bytes.Contains(stripped, []byte("55.75")) {
		t.Fatal("PNG text survived")
	}
	if _, err := png.Decode(bytes.NewReader(stripped)); err != nil {
		t.Fatalf("stripped PNG does not decode: %v", err)
	}
}
