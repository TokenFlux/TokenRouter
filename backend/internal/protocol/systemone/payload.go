package systemone

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// payload.go reads SystemOne image requests and rewrites their routing model
// without changing the representation of uploaded images. The JSON envelope
// is either the complete request body or the first multipart part named
// request. Asset references connect descriptions to inline bytes or uniquely
// named file parts, so validation checks those relationships before routing.
//
// Multipart model replacement rebuilds parts with the original boundary and
// raw part contents. It never converts file uploads to base64, and it returns
// the content type together with the new bytes. Each rewrite starts from the
// caller's unchanged input, allowing retries to choose different model names
// while preserving images and extension fields. Image decoding and model
// support remain upstream responsibilities; local limits bound transfer sizes.

const (
	// ImagesHeader carries the caller's explicit opt-in to TypeSafe image and
	// box evaluation; observing image content never supplies this header implicitly.
	ImagesHeader = "TypeSafe-Danger-Zone-Images"

	// Payload and image bounds follow TypeSafe's wire limits; compressed bodies
	// are checked after shared HTTP decoding, and inline images after base64 decoding.
	MaxPayloadBytes        = 32 << 20
	MaxImageBytes          = 16 << 20
	maxReferenceBytes      = 1024
	maxTotalReferenceBytes = 65536
)

// ErrPayloadTooLarge identifies an oversized decoded SystemOne body so HTTP
// adapters can report 413 before opening an upstream request or billing attempt.
var ErrPayloadTooLarge = errors.New("SystemOne request exceeds 32 MiB")

// Asset describes image bytes carried inline or in a named multipart part.
type Asset struct {
	Type       string `json:"type,omitempty"`        // optional image discriminator
	Format     string `json:"format,omitempty"`      // png, jpg, or jpeg
	DataBase64 string `json:"data_base64,omitempty"` // standard padded base64
	// Part selects multipart bytes in preference to DataBase64. JSON-only
	// requests cannot resolve it, and each part belongs to exactly one asset.
	Part string `json:"part,omitempty"`
}

// payloadPart retains one multipart part's headers and uninterpreted file bytes.
type payloadPart struct {
	header textproto.MIMEHeader // original MIME part headers
	name   string               // multipart form name
	data   []byte               // raw part content
}

// ParsePayload validates a JSON or multipart SystemOne request and its assets.
// An absent content type retains JSON compatibility. Multipart requires its
// JSON envelope first, named request, and each remaining part must be used by
// exactly one image asset. The supplied body remains unchanged for retries.
// @project-doc docs/interfaces/jev_upstream.md#systemone_execution
func ParsePayload(body []byte, contentType string) (Request, error) {
	envelope, parts, _, err := splitPayload(body, contentType)
	if err != nil {
		return Request{}, err
	}
	request, err := parseRequest(envelope)
	if err != nil {
		return request, err
	}
	return request, validateAssets(request, parts)
}

// RequestModel reads routing identity without validating decision questions or images.
// Admission uses it before the protocol handler performs full validation. Missing
// model returns an empty string so the caller can retain its endpoint-specific
// missing-model error; malformed, duplicate-key, or non-object envelopes and
// multipart framing return errors.
func RequestModel(body []byte, contentType string) (string, error) {
	envelope, _, _, err := splitPayload(body, contentType)
	if err != nil {
		return "", err
	}
	if err := wirejson.ValidateRequestObject(envelope); err != nil {
		return "", errors.New("invalid SystemOne JSON request")
	}
	var request struct {
		Model string `json:"model"` // routing model identifier
	}
	if err := json.Unmarshal(envelope, &request); err != nil {
		return "", errors.New("invalid SystemOne JSON request")
	}
	return strings.TrimSpace(request.Model), nil
}

// ReplaceRequestModel returns a new body and its matching content type.
// Callers validate the original payload separately. JSON patches retain
// unknown fields, and multipart patches only the request envelope while
// preserving every image's headers, bytes, order, and original boundary.
//
// No input storage is modified. A failed rebuild returns no replacement,
// keeping the caller's original payload available for diagnostics and later
// attempts. Raw parts avoid MIME transfer decoding that could otherwise
// change file bytes during an unrelated routing-model replacement.
func ReplaceRequestModel(body []byte, contentType, model string) ([]byte, string, error) {
	envelope, parts, boundary, err := splitPayload(body, contentType)
	if err != nil {
		return nil, "", err
	}
	envelope, err = ReplaceModel(envelope, model)
	if err != nil {
		return nil, "", err
	}
	if boundary == "" {
		if len(envelope) > MaxPayloadBytes {
			return nil, "", ErrPayloadTooLarge
		}
		return envelope, "application/json", nil
	}
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, "", err
	}
	for index, part := range parts {
		target, err := writer.CreatePart(part.header)
		if err != nil {
			return nil, "", err
		}
		data := part.data
		if index == 0 {
			data = envelope
		}
		if _, err := target.Write(data); err != nil {
			return nil, "", err
		}
	}
	// Close emits the final boundary before publishing either body or content
	// type, so an incomplete rebuild cannot become the next provider's input.
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	if output.Len() > MaxPayloadBytes {
		return nil, "", ErrPayloadTooLarge
	}
	return output.Bytes(), writer.FormDataContentType(), nil
}

// splitPayload separates the JSON envelope from optional multipart images.
// Empty content type means JSON. Multipart names must be unique and nonblank,
// with request first; unsupported image MIME types fail before asset lookup.
// Returned raw parts borrow no reader resources and require no caller cleanup.
func splitPayload(body []byte, contentType string) ([]byte, []payloadPart, string, error) {
	if len(body) > MaxPayloadBytes {
		return nil, nil, "", ErrPayloadTooLarge
	}
	if contentType == "" {
		contentType = "application/json"
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, nil, "", errors.New("invalid SystemOne content type")
	}
	if mediaType == "application/json" {
		return body, nil, "", nil
	}
	if mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		return nil, nil, "", errors.New("SystemOne requires JSON or multipart/form-data")
	}
	reader := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
	var parts []payloadPart
	names := map[string]bool{}
	for {
		// Read raw MIME parts so routing rewrites cannot silently decode transfer
		// encodings and change the uploaded image bytes or their declared headers.
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, "", errors.New("invalid SystemOne multipart body")
		}
		name := part.FormName()
		if strings.TrimSpace(name) == "" || names[name] || (len(parts) == 0 && name != "request") {
			return nil, nil, "", errors.New("multipart requires request first and unique nonempty part names")
		}
		names[name] = true
		partType := part.Header.Get("Content-Type")
		if partType != "" {
			partType, _, err = mime.ParseMediaType(partType)
			if err != nil {
				return nil, nil, "", errors.New("invalid multipart part content type")
			}
		}
		if len(parts) == 0 {
			if partType != "application/json" {
				return nil, nil, "", errors.New("request part must have application/json content type")
			}
		} else if partType != "" && partType != "image/png" && partType != "image/jpeg" && partType != "image/jpg" && partType != "application/octet-stream" {
			return nil, nil, "", errors.New("image parts require PNG or JPEG content")
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, nil, "", errors.New("invalid SystemOne multipart part")
		}
		if len(parts) > 0 && (len(data) == 0 || len(data) > MaxImageBytes) {
			return nil, nil, "", errors.New("image parts must contain 1 byte to 16 MiB")
		}
		parts = append(parts, payloadPart{header: part.Header, name: name, data: data})
	}
	if len(parts) == 0 {
		return nil, nil, "", errors.New("multipart request part is required")
	}
	return parts[0].data, parts, parameters["boundary"], nil
}

// validateAssets checks references and the chosen image transfer representation.
// Multipart part takes precedence over inline data, matching TypeSafe's API.
// Reference matching decodes and scans each description once for all assets;
// object keys remain text and cannot establish that an image reached the model.
func validateAssets(request Request, parts []payloadPart) error {
	files := map[string]bool{}
	for index, part := range parts {
		if index > 0 {
			files[part.name] = false
		}
	}
	totalReferenceBytes := 0
	for reference, asset := range request.Assets {
		totalReferenceBytes += len(reference)
		if strings.TrimSpace(reference) == "" || len(reference) > maxReferenceBytes || totalReferenceBytes > maxTotalReferenceBytes {
			return errors.New("image references require 1 to 1024 bytes each and at most 65536 bytes together")
		}
		if asset.Type != "" && asset.Type != "image" {
			return fmt.Errorf("assets[%q].type must be image", reference)
		}
		if asset.Format != "" && asset.Format != "png" && asset.Format != "jpg" && asset.Format != "jpeg" {
			return fmt.Errorf("assets[%q].format must be png, jpg, or jpeg", reference)
		}
		if asset.Part != "" {
			// TypeSafe selects the named file before inspecting inline data. Ignored
			// base64 may be invalid without invalidating an otherwise usable multipart asset.
			used, exists := files[asset.Part]
			if !exists || used {
				return fmt.Errorf("assets[%q].part must name one unused image part", reference)
			}
			files[asset.Part] = true
		} else {
			if asset.Format == "" || asset.DataBase64 == "" || strings.ContainsAny(asset.DataBase64, "\r\n\t ") {
				return fmt.Errorf("assets[%q] requires format and standard padded data_base64", reference)
			}
			data, err := base64.StdEncoding.Strict().DecodeString(asset.DataBase64)
			if err != nil || len(data) == 0 || len(data) > MaxImageBytes {
				return fmt.Errorf("assets[%q].data_base64 must encode 1 byte to 16 MiB", reference)
			}
		}
	}
	for name, used := range files {
		if !used {
			return fmt.Errorf("multipart image part %q has no asset", name)
		}
	}
	if len(request.Assets) == 0 {
		return nil
	}
	matcher := newAssetReferences(request.Assets)
	matcher.scan(request.State)
	for _, question := range request.Questions {
		matcher.scan(question.Instructions)
		matcher.scan(question.Criteria)
	}
	if reference, missing := matcher.missing(); missing {
		return fmt.Errorf("assets[%q] must appear in state, instructions, or criteria", reference)
	}
	return nil
}
