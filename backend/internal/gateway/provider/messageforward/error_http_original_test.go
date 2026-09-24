package messageforward_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestAnthropicErrorEntryRuleAndMonitoring 验证原生错误执行与真实 HTTP Adapter 的提交及监控标记。
func TestAnthropicErrorEntryRuleAndMonitoring(t *testing.T) {
	for _, tc := range []struct {
		name        string
		retry, skip bool
	}{
		{name: "normal_skip", skip: true},
		{name: "normal_keep"},
		{name: "retry_skip", retry: true, skip: true},
		{name: "retry_keep", retry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			rule := newNonFailoverPassthroughRule(http.StatusUnprocessableEntity, "invalid schema", http.StatusTeapot, "规则安全消息")
			rule.SkipMonitoring = tc.skip
			gatewayhttp.BindErrorPassthroughService(c, newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{rule}))
			resp := &http.Response{
				StatusCode: http.StatusUnprocessableEntity,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":{"message":"Invalid schema in upstream request"}}`))),
			}
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}
			svc := messageforward.NewRuntime(messageforward.Dependencies{}, messageforward.Options{})
			var result *forwardcore.Result
			var err error
			if tc.retry {
				result, err = messageforward.ErrorForTest(svc, context.Background(), gatewayhttp.NewMessageForwardBoundary(c, nil), account, resp, true)
			} else {
				result, err = messageforward.ErrorForTest(svc, context.Background(), gatewayhttp.NewMessageForwardBoundary(c, nil), account, resp, false)
			}
			require.Nil(t, result)
			require.ErrorContains(t, err, "passthrough rule matched")
			require.Equal(t, http.StatusTeapot, recorder.Code)
			require.True(t, gatewayhttp.IsResponseCommitted(c))
			var payload struct {
				Type  string                         `json:"type"`
				Error struct{ Type, Message string } `json:"error"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
			require.Equal(t, "error", payload.Type)
			require.Equal(t, "upstream_error", payload.Error.Type)
			require.Equal(t, "规则安全消息", payload.Error.Message)
			flag, present := c.Get(gatewayhttp.OpsSkipPassthroughKey)
			require.Equal(t, tc.skip, present)
			if tc.skip {
				require.Equal(t, true, flag)
			}
		})
	}
}
