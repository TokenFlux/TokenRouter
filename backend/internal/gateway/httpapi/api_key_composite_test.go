package httpapi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

// SystemOne composite-prefix failures use the decision endpoint's model error
// envelope, including its machine-readable code. Endpoint matching accepts a
// trailing slash while keeping unrelated SystemOne-like paths out of that response choice.

// TestSystemOneCompositeErrorUsesDecisionEndpoint exercises the decision path
// with and without a trailing slash. Both requests must abort with a model
// parameter error and the composite-prefix-required code, while other paths
// retain their own endpoint classification.
func TestSystemOneCompositeErrorUsesDecisionEndpoint(t *testing.T) {
	for _, path := range []string{EndpointSystemOne, EndpointSystemOne + "/"} {
		require.True(t, IsSystemOneEndpoint(path))
		require.False(t, IsOpenAICompositeEndpoint(path))
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, path, nil)
		AbortCompositeKeyError(c, apikey.ErrCompositeKeyPrefixRequired)
		require.True(t, c.IsAborted())
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Contains(t, rec.Body.String(), `"code":"COMPOSITE_KEY_MODEL_PREFIX_REQUIRED"`)
		require.Contains(t, rec.Body.String(), `"param":"model"`)
	}
	require.False(t, IsSystemOneEndpoint("/v1/chat/completions"))
	require.False(t, IsSystemOneEndpoint("/other/systemone"))
	require.True(t, IsOpenAICompositeEndpoint("/v1/chat/completions"))
}

// TestSystemOneModelExtractionDefersQuestionValidation preserves admission's
// missing-model error while reading routing identity from incomplete questions.
// The SystemOne endpoint owns full question validation after key selection.
func TestSystemOneModelExtractionDefersQuestionValidation(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, EndpointSystemOne, strings.NewReader(`{"model":"GPT/alias","questions":{}}`))
	model, err := CompositeModelFromRequest(request)
	require.NoError(t, err)
	require.Equal(t, "GPT/alias", model)
	request = httptest.NewRequest(http.MethodPost, EndpointSystemOne, strings.NewReader(`{"state":"test"}`))
	_, err = CompositeModelFromRequest(request)
	require.ErrorIs(t, err, apikey.ErrCompositeKeyPrefixRequired)
}

// TestCompositeModelFromRequestRejectsAmbiguousJSONAndMultipartWithBadRequest
// sends duplicate JSON model keys and duplicate or uppercase multipart model
// parts. Composite model extraction must return bad-request errors that
// AbortCompositeKeyError maps to HTTP 400 instead of 500.
func TestCompositeModelFromRequestRejectsAmbiguousJSONAndMultipartWithBadRequest(t *testing.T) {
	for _, raw := range []string{
		`{"model":"GroupA/cheap","model":"GroupB/expensive"}`,
		`{"model":"GroupA/cheap","Model":"GroupB/expensive"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		_, err := CompositeModelFromRequest(req)
		require.Error(t, err, raw)

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = req
		AbortCompositeKeyError(c, err)
		require.Equal(t, http.StatusBadRequest, rec.Code, raw)
	}

	for _, fields := range [][][2]string{
		{{"model", "GroupA/cheap"}, {"model", "GroupB/expensive"}},
		{{"Model", "GroupA/cheap"}},
	} {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		for _, field := range fields {
			require.NoError(t, writer.WriteField(field[0], field[1]))
		}
		require.NoError(t, writer.Close())

		req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		_, err := CompositeModelFromRequest(req)
		require.Error(t, err)

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = req
		AbortCompositeKeyError(c, err)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}
