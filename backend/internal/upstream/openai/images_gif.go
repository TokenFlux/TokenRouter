package openai

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// images_gif.go validates animated results before image delivery and records
// frame billing units independently of output file counts. A requested GIF
// must contain an actual GIF container; upstream format labels and requested
// frame counts do not substitute for returned bytes.
//
// URL results use the caller's protected download callback, including when
// optional ordinary image backfill is disabled. Signed remote URLs are retained
// while downloaded bytes are added for delivery. Data URLs keep one encoded
// payload and receive the MIME label established by their validated bytes.
// Download or container failure is a terminal result error, so consumers must
// not submit another generation after the provider has produced its response.
// A response-level size is derived only from uniformly sized GIF results;
// otherwise each item retains its own dimensions without a shared size claim.

// ValidateGIFImagesResponse normalizes GIF results and returns actual frame units.
// Static-only documents are unchanged and return zero. Expected GIF documents
// require every result to contain a GIF; callers bound the original body and the
// download callback's byte budget before passing them to this function.
//
// Successful containers contribute their encoded frame count. Static items in
// mixed responses contribute one unit only when another item is a GIF. The
// request-only frame range does not constrain returned containers. Missing or
// invalid output cannot be repaired with the requested count; errors retain any
// frame units validated before the failing item. The top-level size reflects
// matching dimensions across all-GIF results and is removed for mixed formats
// or differing sizes; static-only responses retain their original metadata.
func ValidateGIFImagesResponse(body []byte, expected bool, fetch func(string) (string, error)) ([]byte, int, error) {
	items := gjson.GetBytes(body, "data").Array()
	frames, animated := 0, false
	gifCount := 0
	commonSize := ""
	for index, item := range items {
		encoded := item.Get("b64_json").String()
		rawURL := item.Get("url").String()
		dataURI := strings.HasPrefix(rawURL, "data:image/")
		if encoded == "" && dataURI {
			header, payload, found := strings.Cut(rawURL, ",")
			if found && strings.HasSuffix(header, ";base64") {
				encoded = payload
			}
		}
		isGIF := expected || strings.EqualFold(item.Get("output_format").String(), "gif") || strings.EqualFold(gjson.GetBytes(body, "output_format").String(), "gif") || strings.HasPrefix(encoded, "R0lGOD")
		if !isGIF {
			frames++
			continue
		}
		if encoded == "" {
			if fetch != nil && rawURL != "" && !dataURI {
				var err error
				encoded, err = fetch(rawURL)
				if err != nil {
					return nil, frames, imagesProtocolError("Failed to download GIF output")
				}
			}
		}
		decoded, err := upstream.DecodeBase64Image(encoded)
		if err != nil || decoded.Mime != "image/gif" {
			return nil, frames, imagesProtocolError("Upstream did not return valid GIF output")
		}
		metadata, err := protocol.ParseGIFMetadata(decoded.Bytes)
		if err != nil {
			return nil, frames, imagesProtocolError("Upstream returned an incomplete GIF")
		}
		animated = true
		gifCount++
		frames += metadata.FrameCount
		size := fmt.Sprintf("%dx%d", metadata.Width, metadata.Height)
		if gifCount == 1 {
			commonSize = size
		} else if size != commonSize {
			commonSize = ""
		}
		// Keep the signed URL while adding verified bytes. Subsequent delivery and
		// frame accounting then refer to the same downloaded animation representation.
		prefix := "data." + strconv.Itoa(index) + "."
		if !dataURI || item.Get("b64_json").String() != "" {
			body, err = sjson.SetBytes(body, prefix+"b64_json", encoded)
			if err != nil {
				return nil, frames, err
			}
		}
		if len(body) > 128<<20 {
			return nil, frames, imagesProtocolError("Expanded GIF results exceed the response size limit")
		}
		// Decoded container dimensions and format are authoritative for GIFs,
		// including data URLs whose absent upstream format previously defaulted to PNG.
		body, err = sjson.SetBytes(body, prefix+"size", size)
		if err != nil {
			return nil, frames, err
		}
		body, err = sjson.SetBytes(body, prefix+"output_format", "gif")
		if err != nil {
			return nil, frames, err
		}
		if dataURI {
			body, err = sjson.SetBytes(body, prefix+"url", "data:image/gif;base64,"+encoded)
			if err != nil {
				return nil, frames, err
			}
		}
		body, err = sjson.SetBytes(body, prefix+"frame_count", metadata.FrameCount)
		if err != nil {
			return nil, frames, err
		}
	}
	if !animated {
		return body, 0, nil
	}
	if gifCount == len(items) {
		var err error
		body, err = sjson.SetBytes(body, "output_format", "gif")
		if err != nil {
			return nil, frames, err
		}
	}
	// A requested or upstream-reported canvas can differ from the returned GIF.
	// Publish a shared size only when every result verifies the same dimensions.
	var err error
	if gifCount == len(items) && commonSize != "" {
		body, err = sjson.SetBytes(body, "size", commonSize)
	} else {
		body, err = sjson.DeleteBytes(body, "size")
	}
	if err != nil {
		return nil, frames, err
	}
	// Expanded encoded bytes and their metadata share the native response limit.
	if len(body) > 128<<20 {
		return nil, frames, imagesProtocolError("Expanded GIF results exceed the response size limit")
	}
	return body, frames, nil
}
