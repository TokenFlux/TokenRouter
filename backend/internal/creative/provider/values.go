package provider

import (
	"fmt"
	"io"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// values.go maps creative image options to provider wire values and supplies
// bounded response decoding.
// Creative image tiers and aspect-ratio labels need concrete wire values for
// each provider. OpenAI combines the tier and orientation into fixed pixel
// dimensions, while Grok receives one of its resolution strings and a
// supported Imagine ratio. Several ratio labels intentionally select the same
// dimensions, so these mappings describe request choices rather than exact
// aspect-ratio arithmetic.
//
// The four-K OpenAI cases choose explicit landscape, portrait, and square
// dimensions. Other tiers use a fixed base and a three-to-two orientation
// shape, with unknown ratios choosing square. Shared byte helpers bound
// response reads and decode provider base64, leaving complete image validation
// to consumers that require it. Multipart file extensions follow the source
// MIME label and default to PNG.

// ReadCreativeUpstreamBody delegates a bounded read and returns at most limit
// bytes from body. Nonpositive limits use sixty-four MiB; reaching the cap
// returns the prefix successfully rather than an oversize error.
func ReadCreativeUpstreamBody(body io.Reader, limit int64) ([]byte, error) {
	return upstream.ReadLimitedBody(body, limit)
}

// CreativeOpenAIImageSize chooses fixed pixel dimensions from the normalized
// image tier and trimmed ratio. Four-K has explicit 16:9, 9:16, 4:3, and 3:4
// cases, with all other ratios choosing 2880 square.
//
// Other tiers use 1024 for one-K and 1536 otherwise, selecting square or a
// three-to-two orientation from the supported ratio labels. Consequently
// several labels such as 16:9 and 4:3 map to the same oriented dimensions.
// Callers treat the returned size as this adapter request mapping, not an
// exact ratio conversion.
func CreativeOpenAIImageSize(imageSize, aspectRatio string) string {
	tier := pricing.NormalizeImageBillingTierOrDefault(imageSize)
	ratio := strings.TrimSpace(aspectRatio)
	if tier == pricing.ImageBillingSize4K {
		switch ratio {
		case "16:9":
			return "3840x2160"
		case "9:16":
			return "2160x3840"
		case "4:3":
			return "3264x2448"
		case "3:4":
			return "2448x3264"
		default:
			return "2880x2880"
		}
	}
	// Non-four-K orientation groups use a fixed base, so the returned dimensions
	// share a three-to-two shape across several ratio labels rather than deriving
	// an exact ratio from the label text.
	base := 1024
	if tier != pricing.ImageBillingSize1K {
		base = 1536
	}
	switch ratio {
	case "16:9", "3:2", "4:3", "2:1", "19.5:9", "20:9":
		return fmt.Sprintf("%dx%d", base+base/2, base)
	case "9:16", "3:4", "2:3", "1:2", "9:19.5", "9:20":
		return fmt.Sprintf("%dx%d", base, base+base/2)
	default:
		return fmt.Sprintf("%dx%d", base, base)
	}
}

// CreativeGrokImageResolution returns the xAI resolution string for a
// normalized image tier. One-K maps to 1k, and every other normalized tier
// maps to 2k for the generation adapter.
func CreativeGrokImageResolution(imageSize string) string {
	if pricing.NormalizeImageBillingTierOrDefault(imageSize) == pricing.ImageBillingSize1K {
		return "1k"
	}
	return "2k"
}

// CreativeGrokAspectRatio delegates xAI Imagine ratio normalization for the
// supplied value. Unsupported or blank ratios return empty, allowing creative
// request builders to omit the optional aspect_ratio field.
func CreativeGrokAspectRatio(aspectRatio string) string {
	return grok.NormalizeImagineAspectRatio(aspectRatio)
}

// DecodeBase64Image delegates padded and unpadded standard base64 image
// decoding. The shared helper sniffs a MIME label and defaults unknown
// signatures to image/png; decoding success does not establish complete image
// validity.
func DecodeBase64Image(raw string) (upstream.DecodedImage, error) {
	return upstream.DecodeBase64Image(raw)
}

// CreativeFileExtension chooses the multipart filename suffix from the trimmed
// lowercase MIME label. JPEG uses jpg, WebP and GIF keep their suffix, and every other label
// selects the png default.
func CreativeFileExtension(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}
