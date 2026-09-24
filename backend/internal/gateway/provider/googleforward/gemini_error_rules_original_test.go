package googleforward_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiWriteGeminiMappedError_NoRuleKeepsDefault(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := newGeminiFixture(geminiDependencies{})
	respBody := []byte(`{"error":{"code":422,"message":"Invalid schema for field messages","status":"INVALID_ARGUMENT"}}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 13, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey}}

	err := gatewayhttp.NewGoogleBoundary(c, svc.Options, false).GeminiMappedError(account, http.StatusUnprocessableEntity, "req-2", respBody)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "invalid_request_error", errField["type"])
	assert.Equal(t, "Upstream request failed", errField["message"])
}

func TestGeminiWriteGeminiMappedError_AppliesRuleFor422(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	ruleSvc := newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{newNonFailoverPassthroughRule(http.StatusUnprocessableEntity, "invalid schema", http.StatusTeapot, "Gemini上游失败")})
	gatewayhttp.BindErrorPassthroughService(c, ruleSvc)

	svc := newGeminiFixture(geminiDependencies{})
	respBody := []byte(`{"error":{"code":422,"message":"Invalid schema for field messages","status":"INVALID_ARGUMENT"}}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey}}

	err := gatewayhttp.NewGoogleBoundary(c, svc.Options, false).GeminiMappedError(account, http.StatusUnprocessableEntity, "req-1", respBody)
	require.Error(t, err)
	assert.Equal(t, http.StatusTeapot, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errField["type"])
	assert.Equal(t, "Gemini上游失败", errField["message"])
}

func newNonFailoverPassthroughRule(statusCode int, keyword string, respCode int, customMessage string) *errorpolicy.ErrorPassthroughRule {
	return &errorpolicy.ErrorPassthroughRule{

		ID: 1,

		Name: "non-failover-rule",

		Enabled: true,

		Priority: 1,

		ErrorCodes: []int{statusCode},

		Keywords: []string{keyword},

		MatchMode: errorpolicy.MatchModeAll,

		PassthroughCode: false,

		ResponseCode: &respCode,

		PassthroughBody: false,

		CustomMessage: &customMessage,
	}
}

func TestGeminiWriteGeminiMappedError_SetsResponseCommitted(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := newGeminiFixture(geminiDependencies{})
	body := []byte(`{"error":{"message":"invalid field"}}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 102, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey}}

	err := gatewayhttp.NewGoogleBoundary(c, svc.Options, false).GeminiMappedError(account, http.StatusBadRequest, "req-99", body)
	require.Error(t, err)
	assert.True(t, gatewayhttp.IsResponseCommitted(c), "Gemini path must mark response committed")
}
