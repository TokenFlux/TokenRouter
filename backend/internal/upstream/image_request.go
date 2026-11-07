package upstream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// image_request.go rewrites image models and modes in JSON or multipart bodies
// while retaining the other parsed parts.
// An image request can keep client prompt and upload data while routing
// selects a different upstream model. JSON requests replace the model path
// through raw member values, consolidating duplicate keys before applying the
// selected mode. Multipart requests need a newly framed body because model is a
// text part among uploads and mask parts. Multipart output receives a fresh
// boundary and a matching content type.
//
// Each parsed part keeps its order and cloned header values, with all model
// text parts receiving the supplied model and a missing text field appended at
// the end. A file part named model is copied as an upload. The standard
// multipart reader handles transfer decoding before copying, so parsed payload
// content is carried into the rebuilt body rather than preserving the original
// wire encoding.

// RewriteImageModel replaces the routed model while retaining transport fields.
// Blank models retain their original value. Both JSON and multipart callers
// receive a body and matching content type for the prepared request.
func RewriteImageModel(body []byte, contentType string, model string) ([]byte, string, error) {
	return RewriteImageRequest(body, contentType, model, protocol.ImageModeBoth, false)
}

// RewriteImageRequest applies the selected model and endpoint transport capability.
// Both-mode requests retain the client's transport fields; forced nonstream
// removes stream and partial_images, while forced streaming sets stream and
// suppresses previews for clients awaiting a buffered response.
//
// JSON member values retain raw numeric and extension representations while
// duplicate keys use their last value before overrides apply. Multipart parts
// retain uploaded bytes and headers. Every overridden transport field is unique
// in JSON and consistently replaced or removed from multipart text parts, so
// an upstream parser cannot select a stale client mode from a later part.
func RewriteImageRequest(body []byte, contentType string, model string, mode protocol.ImageStreamMode, clientStream bool) ([]byte, string, error) {
	fields := map[string]*string{}
	model = strings.TrimSpace(model)
	if model != "" {
		fields["model"] = &model
	}
	switch mode {
	case protocol.ImageModeNonStreaming:
		fields["stream"] = nil
		fields["partial_images"] = nil
	case protocol.ImageModeStreaming:
		stream := "true"
		fields["stream"] = &stream
		if !clientStream {
			partial := "0"
			fields["partial_images"] = &partial
		}
	}
	return rewriteImageFields(body, contentType, fields)
}

// RewriteImageGIFOptions writes normalized animation fields without changing uploads.
// The original body remains immutable. JSON and multipart callers receive a
// matching content type after every duplicate animation control has been replaced.
func RewriteImageGIFOptions(body []byte, contentType string, frames, duration int) ([]byte, string, error) {
	format, count, delay := "gif", strconv.Itoa(frames), strconv.Itoa(duration)
	return rewriteImageFields(body, contentType, map[string]*string{"output_format": &format, "frame_count": &count, "frame_duration_ms": &delay})
}

// rewriteImageFields applies typed control replacements to either image encoding.
// Nil values remove every occurrence. String model and format fields retain
// string encoding, while validated numeric and transport controls remain literals.
func rewriteImageFields(body []byte, contentType string, fields map[string]*string) ([]byte, string, error) {
	if len(fields) == 0 {
		return body, contentType, nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil && strings.EqualFold(mediaType, "multipart/form-data") {
		return rewriteImageMultipartFields(body, contentType, fields)
	}
	// Decode only top-level member values as raw JSON. This collapses duplicate
	// control keys before mutation while preserving exact numbers and nested extensions.
	var values map[string]json.RawMessage
	if err := json.Unmarshal(body, &values); err != nil || values == nil {
		return nil, "", fmt.Errorf("image request must be a JSON object")
	}
	for key, value := range fields {
		if value == nil {
			delete(values, key)
		} else if key == "model" || key == "output_format" {
			// Encoding a string cannot fail; unrelated raw member values remain
			// intact until the complete validated object is encoded below.
			values[key], _ = json.Marshal(*value)
		} else {
			// Transport values are validated literals, so retain boolean and numeric
			// JSON types rather than sending their multipart string representations.
			values[key] = json.RawMessage(*value)
		}
	}
	body, err = json.Marshal(values)
	if err != nil {
		return nil, "", fmt.Errorf("encode image request: %w", err)
	}

	return body, contentType, nil
}

// RewriteImageMultipartModel replaces model text parts while retaining uploads.
// It accepts the supplied model spelling exactly and appends a missing model
// field before closing the newly generated multipart boundary.
func RewriteImageMultipartModel(body []byte, contentType string, model string) ([]byte, string, error) {
	return rewriteImageMultipartFields(body, contentType, map[string]*string{"model": &model})
}

// rewriteImageMultipartFields patches text fields and copies uploaded content.
// Nil replacement values remove all matching text parts. Missing nonnil fields
// are appended in a fixed order, and the returned content type owns the new
// boundary. File parts with matching names remain uploads.
//
// Multipart decoding belongs to NextPart, matching the existing model rewrite.
// The writer retains parsed part headers and decoded bytes for unrelated parts.
// Repeated fields are all patched to prevent parser-dependent mode selection,
// while upload headers and their content remain independent from text policy.
func rewriteImageMultipartFields(body []byte, contentType string, fields map[string]*string) ([]byte, string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", fmt.Errorf("parse multipart content-type: %w", err)
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, "", fmt.Errorf("multipart boundary is required")
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	written := map[string]bool{}
	for {
		part, readErr := reader.NextPart()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, "", fmt.Errorf("read multipart body: %w", readErr)
		}
		name := strings.TrimSpace(part.FormName())
		replacement, replace := fields[name]
		replace = replace && part.FileName() == ""
		if replace && replacement == nil {
			// A forced nonstream request must omit every transport-only text field;
			// otherwise some providers reject even an explicitly false stream flag.
			_ = part.Close()
			continue
		}
		target, createErr := writer.CreatePart(CloneMultipartHeader(part.Header))
		if createErr != nil {
			_ = part.Close()
			return nil, "", createErr
		}
		if replace {
			_, err = io.WriteString(target, *replacement)
			written[name] = true
		} else {
			_, err = io.Copy(target, part)
		}
		// Each source part is released before advancing the multipart reader;
		// copying errors remain the primary failure returned by this rewrite.
		_ = part.Close()
		if err != nil {
			return nil, "", err
		}
	}
	for _, key := range []string{"model", "stream", "partial_images", "output_format", "frame_count", "frame_duration_ms"} {
		if value := fields[key]; value != nil && !written[key] {
			if err := writer.WriteField(key, *value); err != nil {
				return nil, "", err
			}
		}
	}
	// Complete the generated boundary before exposing either the bytes or their
	// content type, so callers cannot submit a partially framed edit request.
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

// CloneMultipartHeader copies a MIME header map and every value slice so the
// caller can modify them independently. Nil input becomes an allocated empty
// map, and nil value slices become allocated empty slices.
func CloneMultipartHeader(src textproto.MIMEHeader) textproto.MIMEHeader {
	dst := make(textproto.MIMEHeader, len(src))
	for key, values := range src {
		copied := make([]string, len(values))
		copy(copied, values)
		dst[key] = copied
	}
	return dst
}
