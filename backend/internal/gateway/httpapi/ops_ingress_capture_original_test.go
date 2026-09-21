package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsCaptureWriterDoesNotCopyIngressRejectBody(t *testing.T) {

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	writer := acquireOpsCaptureWriter(context.Writer)
	defer releaseOpsCaptureWriter(writer)
	writer.setContext(context, opsAccessFixture().Rejected)
	context.Writer = writer
	markOpsIngressRejectedFixture(context)
	context.Status(http.StatusUnauthorized)
	_, err := context.Writer.WriteString(`{"code":"INVALID_API_KEY","message":"Invalid API key"}`)
	require.NoError(t, err)
	require.Empty(t, writer.capturedBytes())
}
