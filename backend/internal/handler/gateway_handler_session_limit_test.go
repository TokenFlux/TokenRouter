//go:build unit

package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 记录真实 handler 链路对会话缓存的操作，不在测试中重写成功/失败的释放判断。
type gatewaySessionLimitCacheStub struct {
	testutil.StubSessionLimitCache
	registered   map[int64][]string
	unregistered map[int64][]string
}

func (s *gatewaySessionLimitCacheStub) RegisterSession(_ context.Context, accountID int64, sessionID string, _ int, _ time.Duration) (bool, error) {
	s.registered[accountID] = append(s.registered[accountID], sessionID)
	return true, nil
}

func (s *gatewaySessionLimitCacheStub) UnregisterSession(_ context.Context, accountID int64, sessionID string) error {
	s.unregistered[accountID] = append(s.unregistered[accountID], sessionID)
	return nil
}

type gatewaySessionUpstreamStub struct {
	respond func(int64) (*http.Response, error)
	called  []int64
}

func (s *gatewaySessionUpstreamStub) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	s.called = append(s.called, accountID)
	return s.respond(accountID)
}

func (s *gatewaySessionUpstreamStub) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxy, accountID, concurrency)
}

func gatewaySessionResponse(status int, stream bool) *http.Response {
	contentType := "application/json"
	body := `{"id":"msg_session","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1},"stop_reason":"end_turn"}`
	if status >= 400 {
		body = `{"type":"error","error":{"type":"invalid_request_error","message":"request rejected"}}`
	} else if stream {
		contentType = "text/event-stream"
		body = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_session\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":1}}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

// 使用真实调度、Forward 和 handler 收尾，只替换外部上游、Redis 与账单依赖。
func newGatewaySessionLimitFixture(t *testing.T, accountType string, failover bool, upstream *gatewaySessionUpstreamStub) (*messageEndpointsFixture, *apikey.APIKey, *gatewaySessionLimitCacheStub) {
	t.Helper()
	groupID := int64(11)
	group := &routing.Group{ID: groupID, Hydrated: true, Platform: capability.PlatformAnthropic, Status: billing.StatusActive}
	accounts := []*gatewayprovider.ExecutionAccount{{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "session-test", Platform: capability.PlatformAnthropic, Type: accountType,
		Credentials: map[string]any{"access_token": "test-token"}, Extra: map[string]any{"max_sessions": 1},
		Concurrency: 2, Status: billing.StatusActive, Schedulable: true,
		AccountGroups: []accountcore.GroupMembership{{AccountID: 12, GroupID: groupID}}},
	}}
	if failover {
		second := *accounts[0]
		second.Record.ID, second.Record.Priority = 13, 1
		second.Record.AccountGroups = []accountcore.GroupMembership{{AccountID: second.Record.ID, GroupID: groupID}}
		accounts = append(accounts, &second)
	}
	sessions := &gatewaySessionLimitCacheStub{registered: make(map[int64][]string), unregistered: make(map[int64][]string)}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	snapshots := scheduler.NewSnapshotService(&fakeSchedulerCache{accounts: accounts}, nil, nil, nil, nil, scheduler.SnapshotBindings{})
	billingCache := newBillingEligibilityFixture(cfg)
	billingCache.Start()
	t.Cleanup(billingCache.Stop)
	completionInput1 := billingtestkit.Calculator(cfg.Default.RateMultiplier, nil, nil)
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, cfg, snapshots, nil, nil, nil, upstream, nil, nil, sessions, sessions,
		nil, nil, nil, nil, nil, nil, responseHeaderFilterForTest(cfg),
	)
	gateway.BindCompletionRecorder(newHTTPCompletionFixture(cfg, nil, completionInput1, billingCache, nil,
		nil, nil, false))

	h := newMessageEndpointsFixture(gateway, newFundingAdmissionFixture(billingCache, cfg), gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(&fakeConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
		Event: logging.Event,
	},
	), gatewayhttp.SSEPingFormatClaude, 0), gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: gatewayMaxBodySize(cfg), MaxSwitches: 1, MaxGeminiSwitches: 0})
	key := &apikey.APIKey{
		ID: 21, UserID: 22, GroupID: &groupID, Status: billing.StatusActive, Group: group,
		User: &identity.User{ID: 22, Concurrency: 10, Balance: 100},
	}
	return h, key, sessions
}

func serveGatewaySessionMessage(h *messageEndpointsFixture, key *apikey.APIKey, stream bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := `{"model":"claude-sonnet-4-5","max_tokens":256,"messages":[{"role":"user","content":"check session lifecycle"}]`
	if stream {
		body += `,"stream":true`
	}
	body += `}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request = c.Request.WithContext(requeststate.WithGroup(c.Request.Context(), key.Group))
	c.Set(string(keyhttp.ContextKeyAPIKey), key)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: key.UserID, Concurrency: 10})
	h.Messages(c)
	return recorder
}

func TestGatewayHandlerMessages_SessionSlotLifecycle(t *testing.T) {

	for _, accountType := range []string{capability.AccountTypeOAuth, capability.AccountTypeSetupToken} {
		for _, tc := range []struct {
			name         string
			stream       bool
			upstreamCode int
			transportErr bool
			failover     bool
		}{
			{name: "non_stream_success", upstreamCode: http.StatusOK},
			{name: "stream_success", stream: true, upstreamCode: http.StatusOK},
			{name: "request_rejected", upstreamCode: http.StatusBadRequest},
			{name: "transport_failure", transportErr: true},
			{name: "failover_success", upstreamCode: http.StatusInternalServerError, failover: true},
		} {
			t.Run(accountType+"/"+tc.name, func(t *testing.T) {
				upstream := &gatewaySessionUpstreamStub{respond: func(accountID int64) (*http.Response, error) {
					if tc.transportErr {
						return nil, errors.New("test upstream connection failed")
					}
					status := tc.upstreamCode
					if tc.failover && accountID == 13 {
						status = http.StatusOK
					}
					return gatewaySessionResponse(status, tc.stream), nil
				}}
				h, key, sessions := newGatewaySessionLimitFixture(t, accountType, tc.failover, upstream)
				response := serveGatewaySessionMessage(h, key, tc.stream)
				require.NotEmpty(t, sessions.registered[12])
				if tc.failover {
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					require.Equal(t, []int64{12, 13}, upstream.called)
					require.Equal(t, sessions.registered[12], sessions.unregistered[12], "失败账号的槽必须释放")
					require.NotEmpty(t, sessions.registered[13])
					require.Empty(t, sessions.unregistered[13], "成功账号的槽必须保留")
					return
				}
				require.Equal(t, []int64{12}, upstream.called)
				if tc.upstreamCode == http.StatusOK {
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					require.Contains(t, response.Body.String(), "ok")
					require.Empty(t, sessions.unregistered, "成功会话必须保留到空闲超时")
				} else {
					require.GreaterOrEqual(t, response.Code, http.StatusBadRequest)
					require.Equal(t, sessions.registered[12], sessions.unregistered[12], "未成功服务的会话必须立即释放")
				}
			})
		}
	}
}
