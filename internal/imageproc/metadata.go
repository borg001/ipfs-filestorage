package imageproc

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
)

// StripMetadata removes what a picture says about where and how it was taken
// - EXIF (with its GPS position), XMP, IPTC and comments - from a JPEG, PNG or
// WebP original before it is stored. The original was stored byte for byte,
// so a phone photo handed the place it was shot to everyone served the
// original. A JPEG keeps its orientation in a minimal EXIF block of its own,
// or a portrait photo would lie on its side. Anything that cannot be read is
// returned unchanged.
func StripMetadata(data []byte) []byte {
	switch {
	case len(data) > 4 && data[0] == 0xFF && data[1] == 0xD8:
		return stripJPEGMetadata(data)
	case len(data) > 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return stripPNGMetadata(data)
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return stripWebPMetadata(data)
	}
	return data
}

func stripJPEGMetadata(data []byte) []byte {
	orientation := jpegOrientation(data)
	out := make([]byte, 0, len(data))
	out = append(out, 0xFF, 0xD8)
	if orientation > 1 && orientation <= 8 {
		out = append(out, jpegOrientationSegment(orientation)...)
	}
	i := 2
	for i+1 < len(data) {
		if data[i] != 0xFF {
			return data
		}
		marker := data[i+1]
		switch {
		case marker == 0xFF:
			i++
			continue
		case marker == 0xDA || marker == 0xD9:
			// Start of scan: the picture itself follows, untouched.
			return append(out, data[i:]...)
		case marker >= 0xD0 && marker <= 0xD7 || marker == 0x01:
			out = append(out, data[i:i+2]...)
			i += 2
			continue
		}
		if i+4 > len(data) {
			return data
		}
		length := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if length < 2 || i+2+length > len(data) {
			return data
		}
		segment := data[i : i+2+length]
		// APP1 carries EXIF and XMP, APP13 the IPTC block, COM free text.
		// ICC colour profiles (APP2) and the Adobe colour marker (APP14)
		// describe how the picture looks and stay.
		if marker != 0xE1 && marker != 0xED && marker != 0xFE {
			out = append(out, segment...)
		}
		i += 2 + length
	}
	return data
}

// jpegOrientation reads the EXIF orientation tag (0x0112), or 0.
func jpegOrientation(data []byte) int {
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 0
		}
		marker := data[i+1]
		if marker == 0xDA || marker == 0xD9 {
			return 0
		}
		if marker >= 0xD0 && marker <= 0xD7 || marker == 0x01 || marker == 0xFF {
			i += 2
			continue
		}
		length := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if length < 2 || i+2+length > len(data) {
			return 0
		}
		payload := data[i+4 : i+2+length]
		if marker == 0xE1 && len(payload) > 14 && string(payload[:6]) == "Exif\x00\x00" {
			if value := tiffOrientation(payload[6:]); value > 0 {
				return value
			}
		}
		i += 2 + length
	}
	return 0
}

func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	offset := int(order.Uint32(tiff[4:8]))
	if offset < 8 || offset+2 > len(tiff) {
		return 0
	}
	count := int(order.Uint16(tiff[offset : offset+2]))
	for entry := 0; entry < count; entry++ {
		start := offset + 2 + entry*12
		if start+12 > len(tiff) {
			return 0
		}
		if order.Uint16(tiff[start:start+2]) == 0x0112 {
			return int(order.Uint16(tiff[start+8 : start+10]))
		}
	}
	return 0
}

// jpegOrientationSegment is an APP1 block holding the orientation alone.
func jpegOrientationSegment(orientation int) []byte {
	tiff := []byte{
		'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08, // header, IFD0 at 8
		0x00, 0x01, // one entry
		0x01, 0x12, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01, 0x00, byte(orientation), 0x00, 0x00, // orientation, SHORT
		0x00, 0x00, 0x00, 0x00, // no next IFD
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	return append(segment, payload...)
}

func stripPNGMetadata(data []byte) []byte {
	out := make([]byte, 0, len(data))
	out = append(out, data[:8]...)
	i := 8
	for i+12 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[i : i+4]))
		if length < 0 || i+12+length > len(data) {
			return data
		}
		kind := string(data[i+4 : i+8])
		chunk := data[i : i+12+length]
		switch kind {
		case "eXIf", "tEXt", "zTXt", "iTXt", "tIME":
		default:
			out = append(out, chunk...)
		}
		i += 12 + length
		if kind == "IEND" {
			return out
		}
	}
	return data
}

func stripWebPMetadata(data []byte) []byte {
	out := make([]byte, 0, len(data))
	out = append(out, data[:12]...)
	i := 12
	for i+8 <= len(data) {
		kind := string(data[i : i+4])
		size := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		padded := size + size%2
		if size < 0 || i+8+padded > len(data) {
			if i+8+size == len(data) {
				padded = size
			} else {
				return data
			}
		}
		chunk := data[i : i+8+padded]
		switch kind {
		case "EXIF", "XMP ":
		case "VP8X":
			cleaned := append([]byte{}, chunk...)
			if len(cleaned) > 8 {
				cleaned[8] &^= 0x08 | 0x04 // no EXIF, no XMP
			}
			out = append(out, cleaned...)
		default:
			out = append(out, chunk...)
		}
		i += 8 + padded
	}
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out
}

// pngChunk builds a PNG chunk; used by the tests.
func pngChunk(kind string, payload []byte) []byte {
	chunk := make([]byte, 8, 12+len(payload))
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(payload)))
	copy(chunk[4:8], kind)
	chunk = append(chunk, payload...)
	crc := crc32.ChecksumIEEE(chunk[4:])
	return binary.BigEndian.AppendUint32(chunk, crc)
}
