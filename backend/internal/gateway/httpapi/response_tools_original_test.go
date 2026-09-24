package httpapi_test

import (
	"net/http/httptest"
	"testing"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClearOpenAIResponsesClientToolMappingRemovesStaleContextState(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	gatewayhttp.SetOpenAIResponsesClientToolMapping(c, bridge.ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}})

	gatewayhttp.ClearOpenAIResponsesClientToolMapping(c)

	_, ok := gatewayhttp.OpenAIResponsesClientToolMapping(c)
	require.False(t, ok)
}
