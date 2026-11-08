package openai

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
)

// image_dimensions.go detects image dimensions from encoded headers and
// reconciles reported result sizes.
// Image billing metadata can carry auto dimensions even after image bytes have
// a concrete size. Dimension probing reads bounded Base64 headers and
// recognizes WebP layouts directly, while registered JPEG, PNG, and GIF decoders
// provide configuration sizes without decoding all pixels.
//
// The probe tries padded then unpadded standard Base64 and returns empty text
// when dimensions cannot be read. Reconciliation replaces each available result
// size with detected dimensions, then updates first-result metadata from the
// first retained result. A failed probe keeps existing size metadata, so
// optional dimension correction does not erase the upstream response value.

// MaxOpenAIImageDimensionProbeBytes caps decoded header input for size probing;
// full image decoding remains outside the metadata and billing-size correction path.
const MaxOpenAIImageDimensionProbeBytes int64 = 1 << 20

// DetectOpenAIImageResultSize returns width-by-height text from a bounded
// Base64 image header. It accepts a data-URL wrapper, tries padded and unpadded
// standard Base64, and returns empty text for unreadable or nonpositive
// dimensions.
func DetectOpenAIImageResultSize(encoded string) string {
	payload := strings.TrimSpace(encoded)
	if strings.HasPrefix(strings.ToLower(payload), "data:") {
		comma := strings.IndexByte(payload, ',')
		if comma < 0 || comma+1 >= len(payload) {
			return ""
		}
		payload = strings.TrimSpace(payload[comma+1:])
	}
	if payload == "" {
		return ""
	}

	// Accept both padding spellings used by image responses, retrying header
	// decoding after an unusable spelling while retaining the same decoded-byte budget.
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoded := base64.NewDecoder(encoding, strings.NewReader(payload))
		// Cap decoded header reads before format-specific image size inspection.
		buffered := bufio.NewReader(io.LimitReader(decoded, MaxOpenAIImageDimensionProbeBytes))
		// A partial prefix can still be inspected; short-read errors remain for
		// the image decoder to handle when direct WebP header recognition cannot succeed.
		prefix, _ := buffered.Peek(30)
		if width, height, ok := DetectOpenAIWebPDimensions(prefix); ok {
			return fmt.Sprintf("%dx%d", width, height)
		}
		cfg, _, err := image.DecodeConfig(buffered)
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			continue
		}
		return fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
	}
	return ""
}

// DetectOpenAIWebPDimensions reads positive dimensions from VP8X, VP8, or VP8L
// headers. It checks container signatures and required prefix lengths;
// unsupported, incomplete, or invalid headers return false with zero
// dimensions.
func DetectOpenAIWebPDimensions(header []byte) (int, int, bool) {
	if len(header) < 16 || string(header[:4]) != "RIFF" || string(header[8:12]) != "WEBP" {
		return 0, 0, false
	}

	switch string(header[12:16]) {
	case "VP8X":
		if len(header) < 30 {
			return 0, 0, false
		}
		// Restore pixel counts from zero-based, three-byte canvas dimension fields.
		width := 1 + int(header[24]) + int(header[25])<<8 + int(header[26])<<16
		height := 1 + int(header[27]) + int(header[28])<<8 + int(header[29])<<16
		return width, height, width > 0 && height > 0
	case "VP8 ":
		if len(header) < 30 || string(header[23:26]) != "\x9d\x01\x2a" {
			return 0, 0, false
		}
		// Remove upper flag bits before reporting the packed pixel dimensions.
		width := int(binary.LittleEndian.Uint16(header[26:28]) & 0x3fff)
		height := int(binary.LittleEndian.Uint16(header[28:30]) & 0x3fff)
		return width, height, width > 0 && height > 0
	case "VP8L":
		if len(header) < 25 || header[20] != 0x2f {
			return 0, 0, false
		}
		// Restore pixel counts from zero-based dimensions split across header bytes.
		width := 1 + int(header[21]) + int(header[22]&0x3f)<<8
		height := 1 + int(header[22]>>6) + int(header[23])<<2 + int(header[24]&0x0f)<<10
		return width, height, width > 0 && height > 0
	default:
		return 0, 0, false
	}
}

// ReconcileOpenAIResponsesImageResultSizes updates caller-owned image result
// sizes from readable final bytes. Failed probes retain existing values, and a
// nonnil firstMeta receives the first result size only when that value is
// nonblank.
func ReconcileOpenAIResponsesImageResultSizes(results []OpenAIResponsesImageResult, firstMeta *OpenAIResponsesImageResult) {
	for i := range results {
		// Final image bytes can resolve an upstream auto size. Update metadata from
		// that evidence so billing dimensions describe the produced image when header parsing succeeds.
		if actualSize := DetectOpenAIImageResultSize(results[i].Result); actualSize != "" {
			results[i].Size = actualSize
		}
	}
	if firstMeta == nil || len(results) == 0 {
		return
	}
	if size := strings.TrimSpace(results[0].Size); size != "" {
		firstMeta.Size = size
	}
}
