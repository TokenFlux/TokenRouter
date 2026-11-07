package upstream

import (
	"encoding/base64"
	"io"
)

// image_bytes.go decodes base64 image payloads, detects supported image
// signatures, and reads bounded response bodies.
// Image responses may encode their bytes with or without base64 padding.
// Decoding tries both standard forms, then assigns PNG, JPEG, WebP, or GIF from a
// short signature check; unknown bytes retain the PNG default used by image
// response consumers.

// DecodedImage holds decoded payload bytes and the MIME label selected for
// them.
type DecodedImage struct {
	Bytes []byte // decoded image payload bytes
	Mime  string // selected image MIME type
}

// ImageOutput carries one indexed image payload returned by a platform
// executor.
type ImageOutput struct {
	Index int    // image response output index
	Bytes []byte // returned image payload bytes
	Mime  string // returned image MIME type
}

// ReadLimitedBody reads at most limit bytes from a nonnil reader into new
// storage. Nonpositive limits select sixty-four MiB; reaching the limit
// returns the prefix successfully, so callers cannot infer truncation from the
// error result.
func ReadLimitedBody(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 64 << 20
	}
	// Reaching this cap returns a prefix successfully. Callers needing oversize
	// rejection read an additional byte themselves instead of using this bounded helper.
	return io.ReadAll(io.LimitReader(body, limit))
}

// DecodeBase64Image decodes standard base64 and assigns a recognized image
// MIME label. A failed padded decode retries unpadded standard base64; unknown
// signatures, including empty decoded data, receive image/png for the image
// consumer.
func DecodeBase64Image(raw string) (DecodedImage, error) {
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// Image response consumers accept upstream payloads with omitted padding.
		// Retry the unpadded standard alphabet before returning the final decoding error.
		data, err = base64.RawStdEncoding.DecodeString(raw)
		if err != nil {
			return DecodedImage{}, err
		}
	}
	mime := SniffImageMIME(data)
	// The image consumer still needs a MIME label for unknown byte signatures.
	// Assign its PNG default after decoding; this label does not validate the payload.
	if mime == "" {
		mime = "image/png"
	}
	return DecodedImage{Bytes: data, Mime: mime}, nil
}

// SniffImageMIME reports PNG, JPEG, WebP, or GIF from the recognized leading byte
// signatures. Unknown or insufficient bytes return empty; this signature check
// does not decode or validate a complete image.
func SniffImageMIME(data []byte) string {
	// These short signature checks supply output labels for the image consumer;
	// they accept matching prefixes even when the rest of the payload is malformed.
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}
