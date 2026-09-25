package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountpolicy "github.com/TokenFlux/TokenRouter/internal/account"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayService_GetCodexClientRestrictionDetector(t *testing.T) {

	t.Run("使用注入的 detector", func(t *testing.T) {
		expected := &requestClientDetectorFixture{
			result: accountpolicy.CodexClientRestrictionDetectionResult{Enabled: true, Matched: true, Reason: "stub"},
		}
		svc := &OpenAIRequests{Detector: expected}

		got := svc.clientDetector()
		require.Same(t, expected, got)
	})

	t.Run("service 为 nil 时返回默认 detector", func(t *testing.T) {
		var svc *OpenAIRequests
		got := svc.clientDetector()
		require.NotNil(t, got)
	})

	t.Run("service 未注入 detector 时返回默认 detector", func(t *testing.T) {
		svc := &OpenAIRequests{Options: OpenAIRequestOptions{ForceCLI: true}}
		got := svc.clientDetector()
		require.NotNil(t, got)

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set("User-Agent", "curl/8.0")
		account := &gatewayprovider.ExecutionAccount{Record: accountpolicy.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Extra: map[string]any{"codex_cli_only": true}}}

		result := got.DetectClient(func() (string, string) { return c.GetHeader("User-Agent"), c.GetHeader("originator") }, gatewayprovider.ExecutionRecord(account), nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountpolicy.CodexClientRestrictionReasonForceCodexCLI, result.Reason)
	})
}

type requestClientDetectorFixture struct {
	result accountpolicy.CodexClientRestrictionDetectionResult
}

func (s *requestClientDetectorFixture) DetectClient(read func() (string, string), record *accountpolicy.Record, allowed []string, matched bool) accountpolicy.CodexClientRestrictionDetectionResult {
	return s.result
}
