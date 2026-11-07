package creative

import "github.com/TokenFlux/TokenRouter/internal/protocol"

// execution_values.go validates and deduplicates generated images before
// selecting the retained creative output.
// A creative execution delivers one retained image even when an upstream
// returns several outputs. Empty byte slices and repeated byte hashes are
// removed, but the size of every nonempty candidate is checked before the
// first retained image is selected. An oversized later image therefore fails
// the complete normalization instead of being hidden by the single-output
// limit.
//
// The returned output structs receive fresh sequential indexes while retaining
// their supplied byte slices and MIME labels. This step establishes the worker
// output count and indexes; image signature detection and provider-specific
// decoding take place in the platform adapters before normalization. Callers
// keep the retained bytes stable while result delivery consumes them.

// CreativeMaxOutputBytes caps each decoded execution image at thirty-two MiB
// before the worker records successful delivery metadata.
const CreativeMaxOutputBytes = 32 << 20

// NormalizeCreativeOutputs returns the first nonempty distinct image with
// index zero, or a nonretryable error when no image survives. The returned
// struct retains the input byte slice, so callers keep those bytes stable
// until delivery finishes.
//
// Every nonempty candidate is size-checked and hashed before the result is
// narrowed to one image. This ordering rejects an oversized later candidate
// even when an earlier candidate is usable. Hash equality removes repeated
// byte content regardless of its supplied MIME label or original index.
func NormalizeCreativeOutputs(outputs []CreativeOutput) ([]CreativeOutput, error) {
	if len(outputs) == 0 {
		return nil, CreativeNonRetryableError("platform returned no image output")
	}
	seen := make(map[string]struct{}, len(outputs))
	out := make([]CreativeOutput, 0, len(outputs))
	for _, output := range outputs {
		if len(output.Bytes) == 0 {
			continue
		}
		// Validate every nonempty candidate before narrowing the
		// result, so a later
		// oversized image cannot escape the worker size limit behind an
		// earlier one.
		if len(output.Bytes) > CreativeMaxOutputBytes {
			return nil, CreativeNonRetryableError("creative output %d exceeds size limit %d bytes", output.Index, CreativeMaxOutputBytes)
		}
		// Provider indexes and MIME labels can differ for identical
		// bytes. Use the
		// byte hash as the duplicate identity before assigning the
		// worker delivery index.
		sum := Sha256Hex(output.Bytes)
		if _, ok := seen[sum]; ok {
			continue
		}
		if output.Mime == "image/gif" {
			if _, err := protocol.ParseGIFMetadata(output.Bytes); err != nil {
				return nil, CreativeImageResultError("INVALID_IMAGE_RESPONSE", "platform returned an incomplete GIF")
			}
		}
		seen[sum] = struct{}{}
		out = append(out, output)
	}
	if len(out) == 0 {
		return nil, CreativeNonRetryableError("platform returned no usable image output")
	}
	// Worker delivery uses one indexed output per execution. Narrow the validated
	// candidates here so provider surplus images do not increase captured output
	// count or create additional delivery indexes.
	if len(out) > 1 {
		out = out[:1]
	}
	for index := range out {
		out[index].Index = index
	}
	return out, nil
}
