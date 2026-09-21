// Package testkit 提供网关契约测试的可控客户端和原生装配，不复制请求编排。
package testkit

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// QoderClient 记录原始请求和响应夹具，生产执行仍由 upstream.Executor 完成。
type QoderClient struct {
	Request  *http.Request
	Requests []*http.Request
	Body     string
	Bodies   [][]byte
	Err      error
	Headers  map[string]string
}

func (c *QoderClient) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, body []byte, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	c.Request = req
	c.Requests = append(c.Requests, req)
	c.Bodies = append(c.Bodies, append([]byte(nil), body...))
	c.Headers = headers
	if c.Err != nil {
		return nil, c.Err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(c.Body))}, nil
}

func (c *QoderClient) StreamRequestContextWithDoer(ctx context.Context, _ *qoder.SessionContext, _ string, body []byte, headers map[string]string, doer qoder.RequestDoer) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	c.Request = req
	c.Requests = append(c.Requests, req)
	c.Bodies = append(c.Bodies, append([]byte(nil), body...))
	c.Headers = headers
	if c.Err != nil {
		return nil, c.Err
	}
	return doer(req)
}

func (c *QoderClient) BodyAt(index int) []byte {
	if c == nil || index < 0 || index >= len(c.Bodies) {
		return nil
	}
	return append([]byte(nil), c.Bodies[index]...)
}
func (c *QoderClient) BodyCount() int {
	if c == nil {
		return 0
	}
	return len(c.Bodies)
}

// QoderFixture 保存测试绑定，客户端替换仅供原有阻塞流夹具在执行前配置。
type QoderFixture struct {
	Runtime *gatewayprovider.QoderRuntime
	Tokens  *accountprovider.QoderTokenProvider
	Client  qoder.StreamClient
}

type qoderFixtureClient struct{ fixture *QoderFixture }

func (c qoderFixtureClient) StreamRequestContext(ctx context.Context, session *qoder.SessionContext, path string, body []byte, headers map[string]string) (*http.Response, error) {
	return c.fixture.Client.StreamRequestContext(ctx, session, path, body, headers)
}

// NewQoderFixture 构造唯一运行时；不提供旧 Service 方法或另一套循环。
func NewQoderFixture(tokens *accountprovider.QoderTokenProvider, client qoder.StreamClient, conversations *qoder.QoderConversationStore) *QoderFixture {
	f := &QoderFixture{Tokens: tokens, Client: client}
	f.Runtime = gatewayprovider.NewQoderRuntime(gatewayprovider.QoderRuntimeOptions{Tokens: tokens, Client: qoderFixtureClient{fixture: f}, Conversations: conversations})
	return f
}

func NewDefaultQoderFixture() (*account.Record, *QoderFixture, *QoderClient) {
	value := &account.Record{ID: 8801, Name: "qoder", Platform: account.PlatformQoder, Type: account.AccountTypeCosy, Credentials: map[string]any{}}
	client := &QoderClient{Body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":2}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"}
	tokens := accountprovider.NewQoderTokenProvider(qoder.SessionBuilder{})
	tokens.Core.Sessions = map[int64]account.QoderSessionCacheEntry[*qoder.SessionContext]{value.ID: {CredentialsHash: account.QoderCredentialsHash(value.Credentials), Session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}}}}
	return value, NewQoderFixture(tokens, client, qoder.NewQoderConversationStore(qoder.QoderConversationTTL)), client
}
