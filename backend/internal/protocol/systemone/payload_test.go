package systemone

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// payload_test.go checks image transfer validation and multipart model rewrites.
// Image bytes remain opaque fixtures because decoding belongs to TypeSafe;
// the tests protect resource ownership, transfer limits, and preservation of
// the original request across independent routing attempts.

// TestImagePayloadReferences supplies nested image references in all description
// positions. Object keys cannot consume an asset, and moderation excludes its
// encoded data while preserving the surrounding question and state text.
func TestImagePayloadReferences(t *testing.T) {
	for _, content := range []string{
		`"state":{"nested":["look at <photo>"]},"questions":{"a":{"type":"box"}}`,
		`"state":"ready","questions":{"a":{"type":"box","instructions":[{"photo":"<photo>"}]}}`,
		`"state":"ready","questions":{"a":{"type":"noul","criteria":{"true":"<photo>","false":null}}}`,
		`"state":"ready","questions":{"a":{"type":"choice","criteria":{"photo":"<photo>"}}}`,
		`"state":"ready","questions":{"a":{"type":"score","instructions":null,"criteria":[null,"<photo>"]}}`,
	} {
		body := []byte(`{"model":"jev-latest",` + content + `,"assets":{"<photo>":{"format":"png","data_base64":"c2VjcmV0LWltYWdl"}}}`)
		parsed, err := ParseRequest(body)
		require.NoError(t, err)
		require.NotContains(t, string(parsed.ModerationBody()), "c2VjcmV0LWltYWdl")
		require.NotContains(t, string(parsed.ModerationBody()), "data_base64")
	}
	body := []byte(`{"model":"jev","state":{"<photo>":"text"},"questions":{"a":{"type":"box"}},"assets":{"<photo>":{"format":"png","data_base64":"aW1hZ2U="}}}`)
	_, err := ParseRequest(body)
	require.ErrorContains(t, err, "must appear")
}

// TestMultipartModelRewrite retains an image's binary bytes and unknown JSON
// extensions through independent model replacements. The original request is
// unchanged, and part takes precedence when an ignored inline image is invalid.
func TestMultipartModelRewrite(t *testing.T) {
	envelope := `{"model":"group/alias","state":"<photo>","extension":9007199254740993,"assets":{"<photo>":{"part":"photo","data_base64":"ignored"}},"questions":{"a":{"type":"box","instructions":null}}}`
	data := []byte{0, 1, 255, '\r', '\n', 128, 9}
	body, contentType := imageMultipart(t, envelope, []payloadPart{{name: "photo", data: data}})
	before := bytes.Clone(body)
	for _, model := range []string{"jev-1.14.0", "fallback-model"} {
		request, err := ParsePayload(body, contentType)
		require.NoError(t, err)
		require.Equal(t, "group/alias", request.Model)
		rewritten, rewrittenType, err := ReplaceRequestModel(body, contentType, model)
		require.NoError(t, err)
		request, err = ParsePayload(rewritten, rewrittenType)
		require.NoError(t, err)
		require.Equal(t, model, request.Model)
		jsonBody, parts, _, err := splitPayload(rewritten, rewrittenType)
		require.NoError(t, err)
		require.Contains(t, string(jsonBody), `"extension":9007199254740993`)
		require.Len(t, parts, 2)
		require.Equal(t, data, parts[1].data)
		require.Equal(t, "application/octet-stream", parts[1].header.Get("Content-Type"))
	}
	require.Equal(t, before, body)
}

// TestInvalidImagePayloads rejects malformed inline data and unresolved multipart
// relationships. These fixtures exercise the protocol boundary without opening
// transport connections or depending on a provider's image decoder.
func TestInvalidImagePayloads(t *testing.T) {
	for _, asset := range []string{
		`{"type":"audio","format":"png","data_base64":"YQ=="}`,
		`{"format":"webp","data_base64":"YQ=="}`,
		`{"data_base64":"YQ=="}`,
		`{"format":"png","data_base64":"data:image/png;base64,YQ=="}`,
		`{"format":"png","data_base64":"YQ==\n"}`,
		`{"format":"png","data_base64":"YQ"}`,
		`{"format":"png","data_base64":""}`,
		`{"part":"photo"}`,
	} {
		body := []byte(`{"model":"jev","state":"<photo>","questions":{"a":{"type":"box"}},"assets":{"<photo>":` + asset + `}}`)
		_, err := ParseRequest(body)
		require.Error(t, err, asset)
	}
	for _, fixture := range []struct {
		assets string        // JSON image asset map
		parts  []payloadPart // multipart file fixtures
	}{
		{`{"<photo>":{"part":"missing"}}`, []payloadPart{{name: "photo", data: []byte("a")}}},
		{`{"<photo>":{"part":"photo"},"other":{"part":"photo"}}`, []payloadPart{{name: "photo", data: []byte("a")}}},
		{`{"<photo>":{"part":"photo"}}`, []payloadPart{{name: "photo", data: []byte("a")}, {name: "unused", data: []byte("b")}}},
		{`{"<photo>":{"part":"photo"}}`, []payloadPart{{name: "photo", data: []byte("a")}, {name: "photo", data: []byte("b")}}},
		{`{"<photo>":{"part":"photo"}}`, []payloadPart{{name: "photo", data: nil}}},
	} {
		body, contentType := imageMultipart(t, `{"model":"jev","state":["<photo>","other"],"questions":{"a":{"type":"box"}},"assets":`+fixture.assets+`}`, fixture.parts)
		_, err := ParsePayload(body, contentType)
		require.Error(t, err)
	}
	_, err := ParseRequest([]byte(`{"model":"jev","state":"text","questions":{"a":{"type":"box"}}}`))
	require.ErrorContains(t, err, "requires an image")
}

// TestImagePayloadBounds exercises encoded-body and decoded-image limits at their
// boundaries. A smaller compressed transfer cannot bypass the decoded body cap,
// and an oversized reference is rejected independently of its image bytes.
func TestImagePayloadBounds(t *testing.T) {
	_, err := ParseRequest(bytes.Repeat([]byte(" "), MaxPayloadBytes+1))
	require.ErrorIs(t, err, ErrPayloadTooLarge)
	for _, size := range []int{MaxImageBytes, MaxImageBytes + 1} {
		asset := Asset{Format: "png", DataBase64: base64.StdEncoding.EncodeToString(make([]byte, size))}
		request := Request{Model: "jev", State: json.RawMessage(`"<photo>"`), Questions: map[string]Question{"a": {Type: "box"}}, Assets: map[string]Asset{"<photo>": asset}}
		body, err := json.Marshal(request)
		require.NoError(t, err)
		_, err = ParseRequest(body)
		if size == MaxImageBytes {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	reference := strings.Repeat("x", maxReferenceBytes+1)
	err = validateAssets(Request{Assets: map[string]Asset{reference: {Format: "png", DataBase64: "YQ=="}}}, nil)
	require.ErrorContains(t, err, "1024")
}

// TestRequestModelAndParsePayloadRejectAmbiguousEnvelopes submits duplicate
// model keys and uppercase Model variants in JSON and multipart SystemOne
// envelopes. Both admission model extraction and full payload validation must
// reject the ambiguous envelopes before routing or forwarding.
func TestRequestModelAndParsePayloadRejectAmbiguousEnvelopes(t *testing.T) {
	for _, envelope := range []string{
		`{"model":"jev-lite","state":"ok","questions":{"q":{"type":"noul","instructions":"check"}},"model":"jev-pro"}`,
		`{"model":"jev-lite","Model":"jev-pro","state":"ok","questions":{"q":{"type":"noul","instructions":"check"}}}`,
	} {
		_, err := RequestModel([]byte(envelope), "application/json")
		require.Error(t, err, envelope)
		_, err = ParseRequest([]byte(envelope))
		require.Error(t, err, envelope)

		mpBody, mpType := imageMultipart(t, envelope, nil)
		_, err = RequestModel(mpBody, mpType)
		require.Error(t, err, envelope)
		_, err = ParsePayload(mpBody, mpType)
		require.Error(t, err, envelope)
	}
}

// imageMultipart encodes a JSON envelope followed by opaque image test parts.
// Parts retain their supplied order and names, including intentionally invalid
// duplicates. The caller receives matching bytes and content type for parsing.
func imageMultipart(t *testing.T, envelope string, parts []payloadPart) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	all := append([]payloadPart{{name: "request", data: []byte(envelope)}}, parts...)
	for index, part := range all {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, part.name))
		header.Set("Content-Type", "application/octet-stream")
		if index == 0 {
			header.Set("Content-Type", "application/json")
		}
		out, err := writer.CreatePart(header)
		require.NoError(t, err)
		_, err = out.Write(part.data)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}
