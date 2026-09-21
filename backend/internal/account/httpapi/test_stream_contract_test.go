//go:build unit

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/stretchr/testify/require"
)

func TestProcessGeminiStream_EmitsImageEvent(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	run := provider.NewTestRun(t.Context(), make(http.Header), NewTestEventSink(recorder))
	defer run.Cancel()

	stream := strings.NewReader("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"},{\"inlineData\":{\"mimeType\":\"image/png\",\"data\":\"QUJD\"}}]}}]}\n\ndata: [DONE]\n\n")

	err := (provider.TestStreamOutput{}).Gemini(run, stream)
	require.NoError(t, err)

	body := recorder.Body.String()
	require.Contains(t, body, "\"type\":\"content\"")
	require.Contains(t, body, "\"text\":\"ok\"")
	require.Contains(t, body, "\"type\":\"image\"")
	require.Contains(t, body, "\"image_url\":\"data:image/png;base64,QUJD\"")
	require.Contains(t, body, "\"mime_type\":\"image/png\"")
}
