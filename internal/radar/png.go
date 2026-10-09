package radar

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image/png"
)

// Reject metadata that could expand inside a subsequent native decoder. Bounds
// and chunk CRCs are checked without decompression; png.Decode then verifies the
// pixel stream. No original text/ICC/EXIF or animation chunks reach Qt.
func validatePNG(body []byte, width, height int) error {
	invalid := errors.New("invalid radar PNG")
	if len(body) < 8 || len(body) > MaxImageBytes || !bytes.Equal(body[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return invalid
	}
	pixels, ended := false, false
	for offset := 8; offset < len(body); {
		if len(body)-offset < 12 {
			return invalid
		}
		n := uint64(binary.BigEndian.Uint32(body[offset : offset+4]))
		if n > uint64(len(body)-offset-12) {
			return invalid
		}
		length := int(n)
		kind := string(body[offset+4 : offset+8])
		data := body[offset+8 : offset+8+length]
		if crc32.ChecksumIEEE(body[offset+4:offset+8+length]) != binary.BigEndian.Uint32(body[offset+8+length:offset+12+length]) {
			return invalid
		}
		if offset == 8 {
			if kind != "IHDR" || length != 13 || binary.BigEndian.Uint32(data[:4]) != uint32(width) || binary.BigEndian.Uint32(data[4:8]) != uint32(height) || data[8] > 8 {
				return invalid
			}
		} else {
			switch kind {
			case "IDAT":
				pixels = true
			case "IEND":
				if !pixels || length != 0 || offset+12 != len(body) {
					return invalid
				}
				ended = true
			case "PLTE":
				if pixels || length == 0 || length > 768 || length%3 != 0 {
					return invalid
				}
			case "tRNS":
				if pixels || length == 0 || length > 256 {
					return invalid
				}
			case "sRGB":
				if pixels || length != 1 {
					return invalid
				}
			case "gAMA":
				if pixels || length != 4 {
					return invalid
				}
			case "cHRM":
				if pixels || length != 32 {
					return invalid
				}
			case "pHYs":
				if pixels || length != 9 {
					return invalid
				}
			case "sBIT":
				if pixels || length == 0 || length > 4 {
					return invalid
				}
			default:
				return invalid
			}
		}
		offset += length + 12
	}
	if !ended {
		return invalid
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil || img.Bounds().Dx() != width || img.Bounds().Dy() != height {
		return invalid
	}
	return nil
}
