package protocol

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// image_gif.go validates animation controls and reads GIF frame metadata without
// allocating decoded pixel buffers. Image gateways and creative tasks share
// these rules so a file counts as one output while its frames supply image
// billing units. The parser walks bounded input bytes, including color tables
// and extension subblocks, and requires a complete container before reporting
// a count.
//
// Frame rectangles are checked against the logical screen. Compressed pixels
// remain untouched: this parser validates container structure, not LZW image
// content. Keeping that distinction avoids allocating every animation frame
// merely to price an already bounded response or preserve its original bytes.

const (
	// GIF defaults and limits describe the controls shared by Images and Responses.
	GIFDefaultFrameCount      = 9
	GIFDefaultFrameDurationMS = 200
	GIFMinFrameCount          = 2
	GIFMaxFrameCount          = 16
	GIFMinFrameDurationMS     = 20
	GIFMaxFrameDurationMS     = 2000
)

// GIFMetadata describes a complete container without retaining its source bytes.
type GIFMetadata struct {
	Width      int // logical screen width
	Height     int // logical screen height
	FrameCount int // encoded image frame count
}

// NormalizeGIFOptions resolves omitted animation controls and validates explicit
// integers. Non-GIF output rejects either control. GIF frame delays round to
// the nearest ten milliseconds, matching the upstream encoding precision.
func NormalizeGIFOptions(format string, frames, duration *int) (int, int, error) {
	if !strings.EqualFold(strings.TrimSpace(format), "gif") {
		if frames != nil || duration != nil {
			return 0, 0, fmt.Errorf("frame_count and frame_duration_ms require output_format=gif")
		}
		return 0, 0, nil
	}
	f, d := GIFDefaultFrameCount, GIFDefaultFrameDurationMS
	if frames != nil {
		f = *frames
	}
	if duration != nil {
		d = *duration
	}
	if f < GIFMinFrameCount || f > GIFMaxFrameCount {
		return 0, 0, fmt.Errorf("frame_count must be between 2 and 16")
	}
	if d < GIFMinFrameDurationMS || d > GIFMaxFrameDurationMS {
		return 0, 0, fmt.Errorf("frame_duration_ms must be between 20 and 2000")
	}
	// GIF delays use centiseconds, so round milliseconds before forwarding controls.
	d = ((d + 5) / 10) * 10
	return f, d, nil
}

// ParseGIFMetadata checks a GIF87a or GIF89a container and counts its image
// descriptors. It rejects truncated blocks, invalid rectangles, empty animations,
// and trailing bytes. Callers must bound downloaded or decoded input beforehand.
//
// Extensions and image data use the same length-prefixed subblock traversal.
// Color tables are skipped using their encoded size rather than scanning for
// marker bytes, which may also occur inside compressed data. No frame-sized
// allocation occurs, keeping work proportional to the supplied byte length.
func ParseGIFMetadata(data []byte) (GIFMetadata, error) {
	invalid := fmt.Errorf("invalid or incomplete GIF output")
	if len(data) < 14 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return GIFMetadata{}, invalid
	}
	m := GIFMetadata{Width: int(binary.LittleEndian.Uint16(data[6:8])), Height: int(binary.LittleEndian.Uint16(data[8:10]))}
	if m.Width == 0 || m.Height == 0 {
		return GIFMetadata{}, invalid
	}
	pos := 13
	if data[10]&0x80 != 0 {
		// Each packed table entry occupies three RGB bytes, independent of pixels.
		pos += 3 * (1 << ((data[10] & 7) + 1))
	}
	for pos < len(data) {
		marker := data[pos]
		pos++
		switch marker {
		case 0x3b:
			if pos != len(data) || m.FrameCount == 0 {
				return GIFMetadata{}, invalid
			}
			return m, nil
		case 0x21:
			pos++
		case 0x2c:
			if pos+9 > len(data) {
				return GIFMetadata{}, invalid
			}
			x := int(binary.LittleEndian.Uint16(data[pos:]))
			y := int(binary.LittleEndian.Uint16(data[pos+2:]))
			w := int(binary.LittleEndian.Uint16(data[pos+4:]))
			h := int(binary.LittleEndian.Uint16(data[pos+6:]))
			packed := data[pos+8]
			pos += 9
			if w == 0 || h == 0 || x+w > m.Width || y+h > m.Height {
				return GIFMetadata{}, invalid
			}
			if packed&0x80 != 0 {
				pos += 3 * (1 << ((packed & 7) + 1))
			}
			if pos >= len(data) || data[pos] < 2 || data[pos] > 8 {
				return GIFMetadata{}, invalid
			}
			pos++
			m.FrameCount++
		default:
			return GIFMetadata{}, invalid
		}
		// Consume complete subblocks before reading another marker. Searching raw
		// payload bytes for descriptors would count compressed data as additional frames.
		for {
			if pos >= len(data) {
				return GIFMetadata{}, invalid
			}
			n := int(data[pos])
			pos++
			if n == 0 {
				break
			}
			pos += n
		}
	}
	return GIFMetadata{}, invalid
}
