package imageproc

import "testing"

func heifPayload(brand string) []byte {
	payload := append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftyp")...)
	payload = append(payload, []byte(brand)...)
	return append(payload, []byte("mif1miafMiHB")...)
}

// The type of an upload is read from the file itself: a browser reports HEIC
// inconsistently and Go's own content sniffing does not know the format.
func TestIsHEIFReadsTheBrandFromTheFile(t *testing.T) {
	for _, brand := range []string{"heic", "heix", "heim", "hevc", "mif1", "msf1"} {
		if !IsHEIF(heifPayload(brand)) {
			t.Errorf("IsHEIF(%q) = false, want true", brand)
		}
	}
	if IsHEIF(heifPayload("isom")) {
		t.Error("a video brand was taken for a still image")
	}
	if IsHEIF([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}) {
		t.Error("a JPEG was taken for a HEIF still")
	}
	if IsHEIF([]byte("short")) {
		t.Error("a payload too short to carry a brand was accepted")
	}
}

func TestJPEGFilenameSaysWhatTheFileNowIs(t *testing.T) {
	cases := map[string]string{
		"IMG_0042.HEIC": "IMG_0042.jpg",
		"photo.heif":    "photo.jpg",
		"photo":         "photo.jpg",
		".heic":         "image.jpg",
	}
	for source, want := range cases {
		if got := JPEGFilename(source); got != want {
			t.Errorf("JPEGFilename(%q) = %q, want %q", source, got, want)
		}
	}
}
