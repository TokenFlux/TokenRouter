package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// Raw control bytes expand when represented as JSON escapes, so a body that
// fits its input limit can exceed the normalized limit. These cases also keep
// compatibility repair distinct from structural JSON validation by endpoint handlers.

// TestReadLenientJSONRequestBodyWithPreallocUsesGatewayLimit supplies raw control
// bytes whose escaping exceeds the configured normalized size. Reading must
// return MaxBytesError carrying that gateway limit rather than accepting the smaller input size.
func TestReadLenientJSONRequestBodyWithPreallocUsesGatewayLimit(t *testing.T) {
	body := []byte("{\"input\":\"\x00\x00\"}")
	// Permit one escape's growth so the second exceeds the normalized limit.
	limit := int64(len(body) + 5)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	_, err := ReadLenientJSONRequestBodyWithPrealloc(req, limit)
	var maxErr *http.MaxBytesError
	require.True(t, errors.As(err, &maxErr))
	// The gateway limit applies to the normalized body size.
	require.Equal(t, limit, maxErr.Limit)
}

// TestNormalizeLenientJSONRequestBodyAcceptsClientControlCharsInStrings supplies
// null, ANSI escape, and leading BOM fixtures rejected by strict parsing.
// Normalization must produce valid JSON and retain the decoded string values
// with the expected escaped representation.
func TestNormalizeLenientJSONRequestBodyAcceptsClientControlCharsInStrings(t *testing.T) {
	tests := []struct {
		name string // repair scenario label
		body []byte // client JSON bytes
		path string // decoded value path
		// want and wantRaw describe the same string after normalization. The
		// decoded value retains control bytes while the raw JSON spelling contains their escapes.
		want    string // decoded string value
		wantRaw string // escaped JSON spelling
	}{
		{
			name:    "null byte in message content",
			body:    []byte("{\"messages\":[{\"content\":\"hello\x00world\"}]}"),
			path:    "messages.0.content",
			want:    "hello\x00world",
			wantRaw: `"hello\u0000world"`,
		},
		{
			name:    "ansi escape in message content",
			body:    []byte("{\"messages\":[{\"content\":\"hello\x1b[31mred\x1b[0m\"}]}"),
			path:    "messages.0.content",
			want:    "hello\x1b[31mred\x1b[0m",
			wantRaw: `"hello\u001b[31mred\u001b[0m"`,
		},
		{
			name:    "leading UTF-8 BOM",
			body:    []byte("\xef\xbb\xbf{\"input\":\"hello\"}"),
			path:    "input",
			want:    "hello",
			wantRaw: `"hello"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The fixture is invalid under strict JSON parsing.
			require.False(t, gjson.ValidBytes(tt.body))

			got, err := NormalizeLenientJSONRequestBody(tt.body, 1024)
			require.NoError(t, err)
			require.True(t, gjson.ValidBytes(got))

			result := gjson.GetBytes(got, tt.path)
			require.Equal(t, tt.want, result.String())
			require.Equal(t, tt.wantRaw, result.Raw)
		})
	}
}

// TestNormalizeLenientJSONRequestBodyKeepsInvalidStructureInvalid supplies truncated
// JSON and a control byte outside a string. Normalization can succeed as a
// byte transformation, but both outputs must still fail structural JSON validation.
func TestNormalizeLenientJSONRequestBodyKeepsInvalidStructureInvalid(t *testing.T) {
	tests := []struct {
		name string // invalid structure label
		body []byte // malformed client bytes
	}{
		{
			name: "truncated JSON",
			body: []byte("{\"messages\":[{\"content\":\"hello\"}]"),
		},
		{
			name: "control character outside string",
			body: []byte("{\"input\":\"hello\"}\x00"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeLenientJSONRequestBody(tt.body, 1024)
			require.NoError(t, err)
			// Normalization repairs string contents; malformed JSON structure remains invalid.
			require.False(t, gjson.ValidBytes(got))
		})
	}
}

// TestReadLenientJSONRequestBodyWithPreallocAcceptsClientControlChars reads a Chat
// request containing a raw null byte in message content. The adapter must
// return strict-parser-compatible JSON whose decoded content still contains that byte.
func TestReadLenientJSONRequestBodyWithPreallocAcceptsClientControlChars(t *testing.T) {
	body := []byte("{\"model\":\"gpt-5.5\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\x00world\"}]}")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	got, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)
	require.NoError(t, err)
	require.True(t, gjson.ValidBytes(got))
	require.Equal(t, "hello\x00world", gjson.GetBytes(got, "messages.0.content").String())
}

// TestNormalizeLenientJSONRequestBodyEnforcesLimit compares exact-size acceptance
// with oversized input and normalization expansion. Both rejected paths must
// expose MaxBytesError with the configured limit so HTTP handlers report consistent size failures.
func TestNormalizeLenientJSONRequestBodyEnforcesLimit(t *testing.T) {
	t.Run("unchanged body at exact limit", func(t *testing.T) {
		body := []byte(`{"input":"hello"}`)

		got, err := NormalizeLenientJSONRequestBody(body, int64(len(body)))
		require.NoError(t, err)
		require.Equal(t, body, got)
	})

	t.Run("raw body past limit", func(t *testing.T) {
		body := []byte(`{"input":"hello"}`)
		// Place the valid input one byte past its limit.
		limit := int64(len(body) - 1)

		_, err := NormalizeLenientJSONRequestBody(body, limit)
		var maxErr *http.MaxBytesError
		require.ErrorAs(t, err, &maxErr)
		require.Equal(t, limit, maxErr.Limit)
	})

	t.Run("normalization expansion past limit", func(t *testing.T) {
		body := []byte("{\"input\":\"\x00\x00\"}")
		// Permit one escape's growth so the second exceeds the normalized limit.
		limit := int64(len(body) + 5)

		_, err := NormalizeLenientJSONRequestBody(body, limit)
		var maxErr *http.MaxBytesError
		require.True(t, errors.As(err, &maxErr))
		require.Equal(t, limit, maxErr.Limit)
	})
}

// TestSharedBodyReadersRejectAmbiguousJSONAndFormatBoundedMessage verifies that
// both gateway body readers surface RequestFieldError with bounded member names
// while letting non-JSON multipart payloads pass through to media parsers.
func TestSharedBodyReadersRejectAmbiguousJSONAndFormatBoundedMessage(t *testing.T) {
	for _, body := range []string{
		`{"model":"cheap","messages":[],"model":"expensive"}`,
		`{"model":"cheap","messages":[],"Model":"expensive"}`,
		"{\"model\":\"cheap\",\"input\":\"a\x00b\",\"model\":\"expensive\"}",
		"\xef\xbb\xbf" + `{"model":"cheap","model":"expensive"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(body)))
		got, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)
		var fieldErr *wirejson.RequestFieldError
		require.ErrorAs(t, err, &fieldErr)
		require.Nil(t, got)
		require.Contains(t, RequestBodyReadErrorMessage(err), "duplicate JSON key")
	}

	multipartPayload := []byte("--b\r\nContent-Disposition: form-data; name=\"prompt\"\r\n\r\nhi\r\n--b--\r\n")
	mpReq := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(multipartPayload))
	mpReq.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	gotMP, err := ReadRequestBodyWithPrealloc(mpReq)
	require.NoError(t, err)
	require.Equal(t, multipartPayload, gotMP)

	require.Equal(t, "Failed to read request body", RequestBodyReadErrorMessage(errors.New("eof")))
	require.Equal(t, "Failed to parse request body", RequestBodyParseErrorMessage(errors.New("bad json")))
	longMsg := RequestBodyReadErrorMessage(&wirejson.RequestFieldError{Key: strings.Repeat("m", 200)})
	require.Contains(t, longMsg, strings.Repeat("m", 64)+`"`)
	require.NotContains(t, longMsg, strings.Repeat("m", 65))
}
