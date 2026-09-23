package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSetOpenAIClientTransportHTTP(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	require.Equal(t, OpenAIClientTransportHTTP, GetOpenAIClientTransport(c))
}

func TestSetOpenAIClientTransportWS(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	SetOpenAIClientTransport(c, OpenAIClientTransportWS)
	require.Equal(t, OpenAIClientTransportWS, GetOpenAIClientTransport(c))
}
