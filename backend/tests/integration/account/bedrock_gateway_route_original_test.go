//go:build unit

package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newBedrockRoutingTestAccount 使用虚构凭据构造可调度账号，测试不会访问真实 AWS。
func newBedrockRoutingTestAccount(id int64, region string, forceGlobal bool) gatewayprovider.ExecutionAccount {
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeBedrock,
		Status: billing.StatusActive, Schedulable: true, Concurrency: 5, Priority: int(id),
		Credentials: map[string]any{
			"aws_region": region, "auth_mode": "sigv4",
			"aws_access_key_id": "test-akid", "aws_secret_access_key": "test-secret",
		}},
	}
	if forceGlobal {
		account.Record.Credentials["aws_force_global"] = "true"
	}
	return account
}

func newBedrockRoutingTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c
}

func newBedrockRoutingTestUpstream() *bedrockRoutingTransport {
	return &bedrockRoutingTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"test-message","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}
}

// 正式转发与管理员测试必须使用相同 ID；全局推理不改变来源端点和 SigV4 签名范围。
func TestBedrockRegionRouting_ForwardAndAccountTestUseSameRoute(t *testing.T) {
	for _, tc := range []struct {
		name, model, region, wantID string
		global                      bool
	}{
		{name: "大阪地域", model: "claude-opus-4-7", region: "ap-northeast-3", wantID: "jp.anthropic.claude-opus-4-7"},
		{name: "墨尔本地域", model: "claude-opus-4-7", region: "ap-southeast-4", wantID: "au.anthropic.claude-opus-4-7"},
		{name: "东京全局", model: "claude-sonnet-5", region: " ap-northeast-1 ", global: true, wantID: "global.anthropic.claude-sonnet-5"},
	} {
		for _, authMode := range []string{"sigv4", "apikey"} {
			t.Run(tc.name+"/"+authMode, func(t *testing.T) {
				account := newBedrockRoutingTestAccount(1, tc.region, tc.global)
				account.Record.Credentials["auth_mode"] = authMode
				account.Record.Credentials["api_key"] = "test-api-key"
				before, err := json.Marshal(account.Record.Credentials)
				require.NoError(t, err)
				parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef([]byte(`{"model":"`+tc.model+`","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)), capability.PlatformAnthropic)
				require.NoError(t, err)
				forwardUpstream := newBedrockRoutingTestUpstream()
				gateway := gatewayhttp.NewMessagesExecutor(messageforward.NewRuntime(messageforward.Dependencies{Transport: forwardUpstream, Search: gatewayprovider.NewSearchTools(nil, nil)}, messageforward.Options{Configured: true, ResponseReadLimit: 128 * 1024 * 1024, PreserveContentType: true}), nil)
				result, err := gateway.Forward(context.Background(), newBedrockRoutingTestContext(), &account, parsed)
				require.NoError(t, err)
				require.Equal(t, tc.wantID, result.UpstreamModel)
				require.Equal(t, tc.model, result.Model)

				testUpstream := newBedrockRoutingTestUpstream()
				accountTester := &accountprovider.AnthropicAccountTest{Transport: testUpstream}
				run := accountprovider.NewTestRun(context.Background(), make(http.Header), accounthttp.NewTestEventSink(httptest.NewRecorder()))
				defer run.Cancel()
				err = accountTester.ExecuteBedrock(run, run.Context, gatewayprovider.ExecutionRecord(&account), tc.model, "hello")
				require.NoError(t, run.Result(err))
				for _, upstream := range []*bedrockRoutingTransport{forwardUpstream, testUpstream} {
					require.Len(t, upstream.requests, 1)
					require.Equal(t, "bedrock-runtime."+strings.TrimSpace(tc.region)+".amazonaws.com", upstream.lastReq.URL.Host)
					require.Equal(t, "/model/"+tc.wantID+"/invoke", upstream.lastReq.URL.Path)
					if authMode == "apikey" {
						require.Equal(t, "Bearer test-api-key", upstream.lastReq.Header.Get("Authorization"))
					} else {
						require.Contains(t, upstream.lastReq.Header.Get("Authorization"), "/"+strings.TrimSpace(tc.region)+"/bedrock/aws4_request")
					}
				}
				after, err := json.Marshal(account.Record.Credentials)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	}
}

// 无有效路由时不调用上游或写账号状态，管理员仅在确有全局能力时收到开启提示。
func TestBedrockRegionRouting_InvalidRouteStopsBeforeUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, model, region string
		global, hint        bool
	}{
		{name: "可启用全局", model: "claude-sonnet-5", region: "ap-northeast-1", hint: true},
		{name: "型号不支持全局", model: "claude-opus-4-1", region: "us-east-1", global: true},
		{name: "区域未核实", model: "claude-opus-5", region: "ap-northeast-99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := newBedrockRoutingTestAccount(1, tc.region, tc.global)
			upstream := &bedrockRoutingTransport{}
			gateway := gatewayhttp.NewMessagesExecutor(messageforward.NewRuntime(messageforward.Dependencies{Transport: upstream, Search: gatewayprovider.NewSearchTools(nil, nil)}, messageforward.Options{ResponseReadLimit: 128 * 1024 * 1024}), nil)
			parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef([]byte(`{"model":"`+tc.model+`","messages":[{"role":"user","content":"hello"}]}`)), capability.PlatformAnthropic)
			require.NoError(t, err)
			_, err = gateway.Forward(context.Background(), newBedrockRoutingTestContext(), &account, parsed)
			require.Error(t, err)
			require.NotContains(t, err.Error(), tc.region)
			accountTester := &accountprovider.AnthropicAccountTest{Transport: upstream}
			run := accountprovider.NewTestRun(context.Background(), make(http.Header), accounthttp.NewTestEventSink(httptest.NewRecorder()))
			defer run.Cancel()
			err = run.Result(accountTester.ExecuteBedrock(run, run.Context, gatewayprovider.ExecutionRecord(&account), tc.model, ""))
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.region)
			if tc.hint {
				require.Contains(t, err.Error(), "可开启“强制全局”")
			} else {
				require.NotContains(t, err.Error(), "可开启")
			}
			require.Empty(t, upstream.requests)
			require.Equal(t, billing.StatusActive, account.Record.Status)
			require.True(t, account.Record.Schedulable)
		})
	}
}

// 只记录本地签名请求，不访问真实 AWS。
type bedrockRoutingTransport struct {
	lastReq      *http.Request
	lastBody     []byte
	lastProxyURL string
	requests     []*http.Request
	bodies       [][]byte

	resp      *http.Response
	responses []*http.Response
	err       error

	lastTLSProfile *tlsfingerprint.Profile
}

func (u *bedrockRoutingTransport) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.lastReq = req
	u.lastProxyURL = proxyURL
	if req != nil && req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		u.lastBody = b
		u.bodies = append(u.bodies, append([]byte(nil), b...))
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	u.requests = append(u.requests, req)
	if u.err != nil {
		return nil, u.err
	}
	if len(u.responses) > 0 {
		resp := u.responses[0]
		u.responses = u.responses[1:]
		return resp, nil
	}
	return u.resp, nil
}

func (u *bedrockRoutingTransport) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.lastTLSProfile = profile
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}
