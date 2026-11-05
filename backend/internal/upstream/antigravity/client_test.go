package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// Account client fixtures cover Google OAuth exchanges, v1internal
// project/model queries, and shared tier/credit decoding. Some early fixtures
// construct HTTP requests directly, while RealCall tests invoke Client methods
// through a prefix-rewriting transport; these protect different layers of
// behavior.
//
// Endpoint and secret overrides restore shared package state after each
// serialized test. The redirect transport changes the supplied request URL in
// place and borrows its prefix map, so fixture owners keep those values stable
// through dispatch. Cancelled-context tests begin with cancellation already
// selected by the caller, and model-query fixtures exercise positive limits,
// oversize rejection, typed/raw decoding, and configured endpoint recovery.

// defaultFetchAvailableModelsBodyLimit supplies an eight-MiB bounded response
// allowance for model-query fixtures that exercise the actual Client method.
const defaultFetchAvailableModelsBodyLimit int64 = 8 << 20

// timeoutError supplies fixed timeout and temporary signals for network-error
// classification fixtures.
type timeoutError struct{}

// redirectRoundTripper rewrites the supplied request URL in place before
// delegating test transport dispatch.
type redirectRoundTripper struct {
	// redirects borrows source-prefix to fixture-destination mappings; callers
	// keep the map stable during dispatch because matching reads its live
	// entries.
	redirects map[string]string
	// transport receives the same request after optional URL replacement; nil
	// selects http.DefaultTransport, and fixture owners configure it before
	// concurrent use.
	transport http.RoundTripper
}

// TestNewAPIRequestWithURL_普通请求 builds a nonstreaming v1internal request with
// supplied credentials and bytes. Method, route and credential/content headers
// must match the fixture, while the URL omits the SSE query.
func TestNewAPIRequestWithURL_普通请求(t *testing.T) {
	ctx := context.Background()
	baseURL := "https://example.com"
	action := "generateContent"
	token := "test-token"
	body := []byte(`{"prompt":"hello"}`)

	req, err := NewAPIRequestWithURL(ctx, baseURL, action, token, body)
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}

	// Non-streaming actions omit the SSE query parameter.
	expectedURL := "https://example.com/v1internal:generateContent"
	if req.URL.String() != expectedURL {
		t.Errorf("URL 不匹配: got %s, want %s", req.URL.String(), expectedURL)
	}

	if req.Method != http.MethodPost {
		t.Errorf("请求方法不匹配: got %s, want POST", req.Method)
	}

	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type 不匹配: got %s", ct)
	}
	if auth := req.Header.Get("Authorization"); auth != "Bearer test-token" {
		t.Errorf("Authorization 不匹配: got %s", auth)
	}
	if ua := req.Header.Get("User-Agent"); ua != GetUserAgentForContext(context.Background()) {
		t.Errorf("User-Agent 不匹配: got %s, want %s", ua, GetUserAgentForContext(context.Background()))
	}
}

// TestNewAPIRequestWithURL_流式请求 builds the exact streamGenerateContent action.
// The request URL must retain its v1internal route and include alt=sse so the
// transport requests stream framing.
func TestNewAPIRequestWithURL_流式请求(t *testing.T) {
	ctx := context.Background()
	baseURL := "https://example.com"
	action := "streamGenerateContent"
	token := "tok"
	body := []byte(`{}`)

	req, err := NewAPIRequestWithURL(ctx, baseURL, action, token, body)
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}

	expectedURL := "https://example.com/v1internal:streamGenerateContent?alt=sse"
	if req.URL.String() != expectedURL {
		t.Errorf("URL 不匹配: got %s, want %s", req.URL.String(), expectedURL)
	}
}

// TestNewAPIRequestWithURL_空Body constructs a request with empty source bytes.
// Successful construction must contain a nonnil Body value, reflecting the
// no-body reader selected by standard HTTP creation.
func TestNewAPIRequestWithURL_空Body(t *testing.T) {
	ctx := context.Background()
	req, err := NewAPIRequestWithURL(ctx, "https://example.com", "test", "tok", nil)
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}
	if req.Body == nil {
		t.Error("Body 应该非 nil（bytes.NewReader(nil) 会返回空 reader）")
	}
}

// TestNewAPIRequest_使用默认URL builds an API request through the package-default
// wrapper. Its URL must use the configured BaseURL together with the supplied
// action route and ordinary request construction.
func TestNewAPIRequest_使用默认URL(t *testing.T) {
	ctx := context.Background()
	req, err := NewAPIRequest(ctx, "generateContent", "tok", []byte(`{}`))
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}

	expected := BaseURL + "/v1internal:generateContent"
	if req.URL.String() != expected {
		t.Errorf("URL 不匹配: got %s, want %s", req.URL.String(), expected)
	}
}

// TestTierInfo_UnmarshalJSON_字符串格式 decodes a supplier tier string through the
// shared wire alias. The tier ID must match the input, while the asserted Name
// field remains empty in the decoded fixture.
func TestTierInfo_UnmarshalJSON_字符串格式(t *testing.T) {
	data := []byte(`"free-tier"`)
	var tier TierInfo
	if err := tier.UnmarshalJSON(data); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if tier.ID != "free-tier" {
		t.Errorf("ID 不匹配: got %s, want free-tier", tier.ID)
	}
	if tier.Name != "" {
		t.Errorf("Name 应为空: got %s", tier.Name)
	}
}

// TestTierInfo_UnmarshalJSON_对象格式 decodes a supplier tier object containing
// its descriptive fields. Shared wire decoding must retain the fixture
// identity and object metadata used by account consumers.
func TestTierInfo_UnmarshalJSON_对象格式(t *testing.T) {
	data := []byte(`{"id":"g1-pro-tier","name":"Pro","description":"Pro plan"}`)
	var tier TierInfo
	if err := tier.UnmarshalJSON(data); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if tier.ID != "g1-pro-tier" {
		t.Errorf("ID 不匹配: got %s, want g1-pro-tier", tier.ID)
	}
	if tier.Name != "Pro" {
		t.Errorf("Name 不匹配: got %s, want Pro", tier.Name)
	}
	if tier.Description != "Pro plan" {
		t.Errorf("Description 不匹配: got %s, want Pro plan", tier.Description)
	}
}

// TestTierInfo_UnmarshalJSON_null decodes a null tier into its zero-valued
// fixture. Decoding must succeed and leave the tier identity empty for absent
// supplier tier information.
func TestTierInfo_UnmarshalJSON_null(t *testing.T) {
	data := []byte(`null`)
	var tier TierInfo
	if err := tier.UnmarshalJSON(data); err != nil {
		t.Fatalf("反序列化 null 失败: %v", err)
	}
	if tier.ID != "" {
		t.Errorf("null 场景下 ID 应为空: got %s", tier.ID)
	}
}

// TestTierInfo_UnmarshalJSON_空数据 passes empty source bytes to the tier decoder
// directly. The fixture must accept the absence and leave tier identity empty
// instead of requiring an object form.
func TestTierInfo_UnmarshalJSON_空数据(t *testing.T) {
	data := []byte(``)
	var tier TierInfo
	if err := tier.UnmarshalJSON(data); err != nil {
		t.Fatalf("反序列化空数据失败: %v", err)
	}
	if tier.ID != "" {
		t.Errorf("空数据场景下 ID 应为空: got %s", tier.ID)
	}
}

// TestTierInfo_UnmarshalJSON_空格包裹null decodes whitespace around a null tier
// value. Trimming must let the shared decoder accept absence and retain the
// fixture empty identity.
func TestTierInfo_UnmarshalJSON_空格包裹null(t *testing.T) {
	data := []byte(`  null  `)
	var tier TierInfo
	if err := tier.UnmarshalJSON(data); err != nil {
		t.Fatalf("反序列化空格 null 失败: %v", err)
	}
	if tier.ID != "" {
		t.Errorf("空格 null 场景下 ID 应为空: got %s", tier.ID)
	}
}

// TestTierInfo_UnmarshalJSON_通过JSON嵌套结构 decodes string and object tier forms
// within one account response. The nested shared aliases must recover the
// asserted current and paid tier identities.
func TestTierInfo_UnmarshalJSON_通过JSON嵌套结构(t *testing.T) {
	// Tier fields accept both string and object forms in the same response.
	jsonData := `{"currentTier":"free-tier","paidTier":{"id":"g1-ultra-tier","name":"Ultra"}}`
	var resp LoadCodeAssistResponse
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("反序列化嵌套结构失败: %v", err)
	}
	if resp.CurrentTier == nil || resp.CurrentTier.ID != "free-tier" {
		t.Errorf("CurrentTier 不匹配: got %+v", resp.CurrentTier)
	}
	if resp.PaidTier == nil || resp.PaidTier.ID != "g1-ultra-tier" {
		t.Errorf("PaidTier 不匹配: got %+v", resp.PaidTier)
	}
}

// TestGetTier_PaidTier优先 supplies both paid and current tiers in account
// observations. A nonempty paid identity must win, selecting the paid tier for
// the consumer result.
func TestGetTier_PaidTier优先(t *testing.T) {
	resp := &LoadCodeAssistResponse{
		CurrentTier: &TierInfo{ID: "free-tier"},
		PaidTier:    &PaidTierInfo{ID: "g1-pro-tier"},
	}
	if got := resp.GetTier(); got != "g1-pro-tier" {
		t.Errorf("应返回 paidTier: got %s", got)
	}
}

// TestGetTier_回退到CurrentTier supplies current tier data without a usable paid
// tier. Account selection must return the current fixture identity for
// consumers that ask for the active tier.
func TestGetTier_回退到CurrentTier(t *testing.T) {
	resp := &LoadCodeAssistResponse{
		CurrentTier: &TierInfo{ID: "free-tier"},
	}
	if got := resp.GetTier(); got != "free-tier" {
		t.Errorf("应返回 currentTier: got %s", got)
	}
}

// TestGetTier_PaidTier为空ID supplies an empty paid identity beside a current
// tier. Selection must use the current identity because an empty paid
// observation does not qualify for precedence.
func TestGetTier_PaidTier为空ID(t *testing.T) {
	resp := &LoadCodeAssistResponse{
		CurrentTier: &TierInfo{ID: "free-tier"},
		PaidTier:    &PaidTierInfo{ID: ""},
	}
	if got := resp.GetTier(); got != "free-tier" {
		t.Errorf("paidTier.ID 为空时应回退到 currentTier: got %s", got)
	}
}

// TestGetAvailableCredits supplies one paid-tier credit observation with
// string amount and minimum values. The returned slice must contain one entry
// whose numeric getters produce twenty-five and five.
func TestGetAvailableCredits(t *testing.T) {
	resp := &LoadCodeAssistResponse{
		PaidTier: &PaidTierInfo{
			ID: "g1-pro-tier",
			AvailableCredits: []AvailableCredit{
				{
					CreditType:                  "GOOGLE_ONE_AI",
					CreditAmount:                "25",
					MinimumCreditAmountForUsage: "5",
				},
			},
		},
	}

	credits := resp.GetAvailableCredits()
	if len(credits) != 1 {
		t.Fatalf("AI Credits 数量不匹配: got %d", len(credits))
	}
	if credits[0].GetAmount() != 25 {
		t.Errorf("CreditAmount 解析不正确: got %v", credits[0].GetAmount())
	}
	if credits[0].GetMinimumAmount() != 5 {
		t.Errorf("MinimumCreditAmountForUsage 解析不正确: got %v", credits[0].GetMinimumAmount())
	}
}

// TestGetTier_两者都为nil supplies no current or paid tier in account
// observations. The consumer must return its empty tier result rather than
// dereferencing an absent supplier tier.
func TestGetTier_两者都为nil(t *testing.T) {
	resp := &LoadCodeAssistResponse{}
	if got := resp.GetTier(); got != "" {
		t.Errorf("两者都为 nil 时应返回空字符串: got %s", got)
	}
}

// TestTierIDToPlanType compares known, case-varied, blank, and unknown tier
// identities. Known/blank fixtures yield configured plan labels, while the
// unknown fixture retains its supplied identity spelling.
func TestTierIDToPlanType(t *testing.T) {
	tests := []struct {
		tierID string // input supplier tier identity
		want   string // expected selected plan label
	}{
		{"free-tier", "Free"},
		{"g1-pro-tier", "Pro"},
		{"g1-ultra-tier", "Ultra"},
		{"FREE-TIER", "Free"},
		{"", "Free"},
		{"unknown-tier", "unknown-tier"},
	}
	for _, tt := range tests {
		t.Run(tt.tierID, func(t *testing.T) {
			if got := google.AntigravityTierIDToPlanType(tt.tierID); got != tt.want {
				t.Errorf("TierIDToPlanType(%q) = %q, want %q", tt.tierID, got, tt.want)
			}
		})
	}
}

// mustNewClient constructs a client for a test and marks construction failures
// fatal. It uses t.Helper for attribution, returning the configured client so
// callers can inspect proxy and timeout settings.
func mustNewClient(t *testing.T, proxyURL string) *Client {
	t.Helper()
	client, err := NewClient(proxyURL)
	if err != nil {
		t.Fatalf("NewClient(%q) failed: %v", proxyURL, err)
	}
	return client
}

// TestNewClient_无代理 constructs a client with no explicit proxy. The fixture
// must retain the configured ten-second timeout and nil transport that
// delegates standard default HTTP transport behavior.
func TestNewClient_无代理(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient 返回错误: %v", err)
	}
	if client == nil {
		t.Fatal("NewClient 返回 nil")
	}
	if client.httpClient == nil {
		t.Fatal("httpClient 为 nil")
	}
	if client.httpClient.Timeout != clientTimeout {
		t.Errorf("Timeout 不匹配: got %v, want %v", client.httpClient.Timeout, clientTimeout)
	}
	// A nil Transport uses http.DefaultTransport.
	if client.httpClient.Transport != nil {
		t.Error("无代理时 Transport 应为 nil")
	}
}

// TestNewClient_有代理 constructs a client with an explicit proxy URL. Its
// result must contain a nonnil client and transport. This fixture checks their
// presence without inspecting proxy behavior or dial/TLS settings.
func TestNewClient_有代理(t *testing.T) {
	client, err := NewClient("http://proxy.example.com:8080")
	if err != nil {
		t.Fatalf("NewClient 返回错误: %v", err)
	}
	if client == nil {
		t.Fatal("NewClient 返回 nil")
	}
	if client.httpClient.Transport == nil {
		t.Fatal("有代理时 Transport 不应为 nil")
	}
}

// TestNewClient_空格代理 constructs a client with whitespace-only proxy input.
// Normalization must treat the value as absent and retain the fixture default
// client configuration.
func TestNewClient_空格代理(t *testing.T) {
	client, err := NewClient("   ")
	if err != nil {
		t.Fatalf("NewClient 返回错误: %v", err)
	}
	if client == nil {
		t.Fatal("NewClient 返回 nil")
	}
	if client.httpClient.Transport != nil {
		t.Error("空格代理 Transport 应为 nil")
	}
}

// TestNewClient_无效代理URL supplies a malformed proxy URL during client
// construction. Construction must fail rather than returning a configured
// client that silently ignores the invalid proxy.
func TestNewClient_无效代理URL(t *testing.T) {
	_, err := NewClient("://invalid")
	if err == nil {
		t.Fatal("无效代理 URL 应返回错误")
	}
	if !strings.Contains(err.Error(), "invalid proxy URL") {
		t.Errorf("错误信息应包含 'invalid proxy URL': got %s", err.Error())
	}
}

// TestIsConnectionError_nil classifies a nil error through the account
// connection helper. The helper must return false so absence of failure cannot
// itself trigger endpoint recovery.
func TestIsConnectionError_nil(t *testing.T) {
	if IsConnectionError(nil) {
		t.Error("nil 错误不应判定为连接错误")
	}
}

// TestIsConnectionError_超时错误 wraps a fixture timeout inside a network
// operation error. Account classification must recognize the timeout through
// the enclosing error rather than requiring the leaf error directly.
func TestIsConnectionError_超时错误(t *testing.T) {
	// Classify the timeout through its enclosing network operation error.
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &timeoutError{},
	}
	if !IsConnectionError(err) {
		t.Error("超时错误应判定为连接错误")
	}
}

// Error returns the fixed timeout text used by network classification
// fixtures.
func (e *timeoutError) Error() string { return "timeout" }

// Timeout reports true so the fixture satisfies the network timeout predicate.
func (e *timeoutError) Timeout() bool { return true }

// Temporary reports true as the fixture legacy net.Error temporary indicator.
func (e *timeoutError) Temporary() bool { return true }

// TestIsConnectionError_netOpError supplies a network operation failure to
// account classification. The helper must recognize that error shape so
// configured endpoint recovery can consider a next destination.
func TestIsConnectionError_netOpError(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: fmt.Errorf("connection refused"),
	}
	if !IsConnectionError(err) {
		t.Error("net.OpError 应判定为连接错误")
	}
}

// TestIsConnectionError_urlError supplies a URL error to the account
// connection helper. Its broader classification must accept the fixture shape
// used by HTTP client failure wrapping.
func TestIsConnectionError_urlError(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "https://example.com",
		Err: fmt.Errorf("some error"),
	}
	if !IsConnectionError(err) {
		t.Error("url.Error 应判定为连接错误")
	}
}

// TestIsConnectionError_普通错误 supplies an unrelated ordinary error to
// connection classification. The helper must return false because this fixture
// carries neither timeout nor recognized network/URL error shape.
func TestIsConnectionError_普通错误(t *testing.T) {
	err := fmt.Errorf("some random error")
	if IsConnectionError(err) {
		t.Error("普通错误不应判定为连接错误")
	}
}

// TestIsConnectionError_包装的netOpError wraps a network operation error in
// another error value. Account classification must follow the error chain and
// retain its connection-recovery result.
func TestIsConnectionError_包装的netOpError(t *testing.T) {
	inner := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: fmt.Errorf("connection refused"),
	}
	err := fmt.Errorf("wrapping: %w", inner)
	if !IsConnectionError(err) {
		t.Error("被包装的 net.OpError 应判定为连接错误")
	}
}

// TestShouldFallbackToNextURL_连接错误 passes a recognized connection failure to
// account endpoint classification. The result must permit another endpoint
// when the owning operation has a subsequent configured destination.
func TestShouldFallbackToNextURL_连接错误(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("refused")}
	if !shouldFallbackToNextURL(err, 0) {
		t.Error("连接错误应触发 URL 降级")
	}
}

// TestShouldFallbackToNextURL_状态码 compares transient, missing-endpoint,
// success, and nonrecoverable status fixtures. The helper must match the
// configured account-endpoint recovery policy for each numeric HTTP status.
func TestShouldFallbackToNextURL_状态码(t *testing.T) {
	tests := []struct {
		name       string // human-readable HTTP status case name
		statusCode int    // input supplier HTTP status
		want       bool   // expected account endpoint recovery result
	}{
		{"429 Too Many Requests", http.StatusTooManyRequests, true},
		{"408 Request Timeout", http.StatusRequestTimeout, true},
		{"404 Not Found", http.StatusNotFound, true},
		{"500 Internal Server Error", http.StatusInternalServerError, true},
		{"502 Bad Gateway", http.StatusBadGateway, true},
		{"503 Service Unavailable", http.StatusServiceUnavailable, true},
		{"200 OK", http.StatusOK, false},
		{"201 Created", http.StatusCreated, false},
		{"400 Bad Request", http.StatusBadRequest, false},
		{"401 Unauthorized", http.StatusUnauthorized, false},
		{"403 Forbidden", http.StatusForbidden, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldFallbackToNextURL(nil, tt.statusCode)
			if got != tt.want {
				t.Errorf("shouldFallbackToNextURL(nil, %d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

// TestShouldFallbackToNextURL_无错误且200 supplies no transport error and a
// successful HTTP status. Classification must remain false so a usable account
// response stays on its selected endpoint.
func TestShouldFallbackToNextURL_无错误且200(t *testing.T) {
	if shouldFallbackToNextURL(nil, http.StatusOK) {
		t.Error("无错误且 200 不应触发 URL 降级")
	}
}

// TestClient_ExchangeCode_成功 sends a manually constructed authorization-code
// HTTP fixture to a local server. Its form fields and decoded token response
// are checked separately from Client.ExchangeCode RealCall coverage.
func TestClient_ExchangeCode_成功(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("请求方法不匹配: got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type 不匹配: got %s", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("解析表单失败: %v", err)
		}
		if r.FormValue("client_id") != ClientID {
			t.Errorf("client_id 不匹配: got %s", r.FormValue("client_id"))
		}
		if r.FormValue("client_secret") != "test-secret" {
			t.Errorf("client_secret 不匹配: got %s", r.FormValue("client_secret"))
		}
		if r.FormValue("code") != "auth-code" {
			t.Errorf("code 不匹配: got %s", r.FormValue("code"))
		}
		if r.FormValue("code_verifier") != "verifier123" {
			t.Errorf("code_verifier 不匹配: got %s", r.FormValue("code_verifier"))
		}
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("grant_type 不匹配: got %s", r.FormValue("grant_type"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "access-tok",
			ExpiresIn:    3600,
			TokenType:    "Bearer",
			RefreshToken: "refresh-tok",
		})
	}))
	defer server.Close()

	// This fixture exercises the token response using a manually constructed request.
	// The RealCall cases below exercise ExchangeCode through a redirecting transport.
	client := &Client{httpClient: server.Client()}

	originalTokenURL := TokenURL
	_ = originalTokenURL
	_ = client

	ctx := context.Background()
	params := url.Values{}
	params.Set("client_id", ClientID)
	params.Set("client_secret", "test-secret")
	params.Set("code", "auth-code")
	params.Set("redirect_uri", RedirectURI)
	params.Set("grant_type", "authorization_code")
	params.Set("code_verifier", "verifier123")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码不匹配: got %d", resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if tokenResp.AccessToken != "access-tok" {
		t.Errorf("AccessToken 不匹配: got %s", tokenResp.AccessToken)
	}
	if tokenResp.RefreshToken != "refresh-tok" {
		t.Errorf("RefreshToken 不匹配: got %s", tokenResp.RefreshToken)
	}
}

// TestClient_ExchangeCode_无ClientSecret empties the cached OAuth secret before
// invoking code exchange. The client must reject missing credentials before
// dispatch, and cleanup restores shared secret state.
func TestClient_ExchangeCode_无ClientSecret(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = ""
	t.Cleanup(func() { defaultClientSecret = old })

	client := mustNewClient(t, "")
	_, err := client.ExchangeCode(context.Background(), "code", "verifier")
	if err == nil {
		t.Fatal("缺少 client_secret 时应返回错误")
	}
	if !strings.Contains(err.Error(), "provider.antigravity.oauth_client_secret") {
		t.Errorf("错误信息应包含环境变量名: got %s", err.Error())
	}
}

// TestClient_ExchangeCode_服务器返回错误 sends a manually constructed token request
// to an error fixture. The observed HTTP status must match the supplied
// response without claiming Client.ExchangeCode invocation.
func TestClient_ExchangeCode_服务器返回错误(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()

	// Construct the token request directly to check this failure fixture;
	// dedicated RealCall cases invoke client error handling through the redirect
	// transport.
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("状态码不匹配: got %d, want 400", resp.StatusCode)
	}
}

// TestClient_RefreshToken_MockServer sends a manually constructed
// refresh-token form to a local server. Its request fields and decoded token
// observations are checked independently from Client.RefreshToken RealCall
// coverage.
func TestClient_RefreshToken_MockServer(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("请求方法不匹配: got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("解析表单失败: %v", err)
		}
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("grant_type 不匹配: got %s", r.FormValue("grant_type"))
		}
		if r.FormValue("refresh_token") != "old-refresh-tok" {
			t.Errorf("refresh_token 不匹配: got %s", r.FormValue("refresh_token"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken: "new-access-tok",
			ExpiresIn:   3600,
			TokenType:   "Bearer",
		})
	}))
	defer server.Close()

	ctx := context.Background()
	params := url.Values{}
	params.Set("client_id", ClientID)
	params.Set("client_secret", "test-secret")
	params.Set("refresh_token", "old-refresh-tok")
	params.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码不匹配: got %d", resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if tokenResp.AccessToken != "new-access-tok" {
		t.Errorf("AccessToken 不匹配: got %s", tokenResp.AccessToken)
	}
}

// TestClient_RefreshToken_无ClientSecret empties the cached secret before
// invoking refresh-token exchange. The client must reject missing credentials
// before dispatch, and test cleanup restores shared startup state.
func TestClient_RefreshToken_无ClientSecret(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = ""
	t.Cleanup(func() { defaultClientSecret = old })

	client := mustNewClient(t, "")
	_, err := client.RefreshToken(context.Background(), "refresh-tok")
	if err == nil {
		t.Fatal("缺少 client_secret 时应返回错误")
	}
}

// TestClient_GetUserInfo_成功 constructs a user-info HTTP request directly with
// fixture authentication. The local response must decode account observations,
// separately from tests invoking Client.GetUserInfo itself.
func TestClient_GetUserInfo_成功(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("请求方法不匹配: got %s", r.Method)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-access-token" {
			t.Errorf("Authorization 不匹配: got %s", auth)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(UserInfo{
			Email:      "user@example.com",
			Name:       "Test User",
			GivenName:  "Test",
			FamilyName: "User",
			Picture:    "https://example.com/photo.jpg",
		})
	}))
	defer server.Close()

	// Construct the authenticated request directly for this user-info fixture;
	// separate RealCall tests invoke the method through its configured client
	// transport.
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-access-token")

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码不匹配: got %d", resp.StatusCode)
	}

	var userInfo UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if userInfo.Email != "user@example.com" {
		t.Errorf("Email 不匹配: got %s", userInfo.Email)
	}
	if userInfo.Name != "Test User" {
		t.Errorf("Name 不匹配: got %s", userInfo.Name)
	}
}

// TestClient_GetUserInfo_服务器返回错误 constructs a user-info request directly
// against an error fixture. Its observed HTTP failure checks the response
// setup rather than invoking Client.GetUserInfo error handling.
func TestClient_GetUserInfo_服务器返回错误(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer server.Close()

	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("状态码不匹配: got %d, want 401", resp.StatusCode)
	}
}

// TestTokenResponse_JSON序列化 decodes the fixture OAuth token JSON through the
// shared alias. Access, refresh and expiry fields must equal the three supplier
// values asserted by this fixture.
func TestTokenResponse_JSON序列化(t *testing.T) {
	jsonData := `{"access_token":"at","expires_in":3600,"token_type":"Bearer","scope":"openid","refresh_token":"rt"}`
	var resp TokenResponse
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if resp.AccessToken != "at" {
		t.Errorf("AccessToken 不匹配: got %s", resp.AccessToken)
	}
	if resp.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn 不匹配: got %d", resp.ExpiresIn)
	}
	if resp.RefreshToken != "rt" {
		t.Errorf("RefreshToken 不匹配: got %s", resp.RefreshToken)
	}
}

// TestUserInfo_JSON序列化 decodes the fixture Google user JSON through the shared
// alias. The resulting account identity and profile observations must match
// the supplied response fields.
func TestUserInfo_JSON序列化(t *testing.T) {
	jsonData := `{"email":"a@b.com","name":"Alice"}`
	var info UserInfo
	if err := json.Unmarshal([]byte(jsonData), &info); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if info.Email != "a@b.com" {
		t.Errorf("Email 不匹配: got %s", info.Email)
	}
	if info.Name != "Alice" {
		t.Errorf("Name 不匹配: got %s", info.Name)
	}
}

// TestLoadCodeAssistResponse_完整JSON decodes a complete account/project
// response fixture. Shared wire data must retain the asserted project, tier,
// and account metadata needed by account discovery consumers.
func TestLoadCodeAssistResponse_完整JSON(t *testing.T) {
	jsonData := `{
		"cloudaicompanionProject": "proj-123",
		"currentTier": "free-tier",
		"paidTier": {"id": "g1-pro-tier", "name": "Pro"},
		"ineligibleTiers": [{"tier": {"id": "g1-ultra-tier"}, "reasonCode": "INELIGIBLE_ACCOUNT"}]
	}`
	var resp LoadCodeAssistResponse
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if resp.CloudAICompanionProject != "proj-123" {
		t.Errorf("CloudAICompanionProject 不匹配: got %s", resp.CloudAICompanionProject)
	}
	if resp.GetTier() != "g1-pro-tier" {
		t.Errorf("GetTier 不匹配: got %s", resp.GetTier())
	}
	if len(resp.IneligibleTiers) != 1 {
		t.Fatalf("IneligibleTiers 数量不匹配: got %d", len(resp.IneligibleTiers))
	}
	if resp.IneligibleTiers[0].ReasonCode != "INELIGIBLE_ACCOUNT" {
		t.Errorf("ReasonCode 不匹配: got %s", resp.IneligibleTiers[0].ReasonCode)
	}
}

// RoundTrip rewrites a nonnil request URL using the first matching prefix
// encountered in redirects, then delegates dispatch. The request URL is
// modified in place, while other request state remains on the supplied
// request.
//
// Map iteration gives overlapping prefixes no deterministic priority, and
// parsed replacement errors return before dispatch. A nil transport selects
// http.DefaultTransport; the transport receives the same request pointer.
// Callers provide exclusive request ownership and keep the borrowed prefix map
// stable while dispatch is running.
func (rt *redirectRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	originalURL := req.URL.String()
	for prefix, target := range rt.redirects {
		if after, ok := strings.CutPrefix(originalURL, prefix); ok {
			newURL := target + after
			parsed, err := url.Parse(newURL)
			if err != nil {
				return nil, err
			}
			// Change the supplied request in place after the first iterated prefix
			// match, retaining headers/body/context while callers provide stable
			// mapping and exclusive request ownership.
			req.URL = parsed
			break
		}
	}
	if rt.transport == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return rt.transport.RoundTrip(req)
}

// newTestClientWithRedirect constructs a ten-second client borrowing redirects
// for its test transport. Matching requests are rewritten in place and sent
// through the default transport; callers keep mappings stable until fixture
// requests finish.
func newTestClientWithRedirect(redirects map[string]string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &redirectRoundTripper{
				redirects: redirects,
			},
		},
	}
}

// TestClient_ExchangeCode_Success_RealCall invokes Client.ExchangeCode through
// a redirecting transport. The local server checks authorization-code and PKCE
// form inputs, and the client must return decoded token observations.
func TestClient_ExchangeCode_Success_RealCall(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("请求方法不匹配: got %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type 不匹配: got %s", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("解析表单失败: %v", err)
		}
		if r.FormValue("client_id") != ClientID {
			t.Errorf("client_id 不匹配: got %s", r.FormValue("client_id"))
		}
		if r.FormValue("client_secret") != "test-secret" {
			t.Errorf("client_secret 不匹配: got %s", r.FormValue("client_secret"))
		}
		if r.FormValue("code") != "test-auth-code" {
			t.Errorf("code 不匹配: got %s", r.FormValue("code"))
		}
		if r.FormValue("code_verifier") != "test-verifier" {
			t.Errorf("code_verifier 不匹配: got %s", r.FormValue("code_verifier"))
		}
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("grant_type 不匹配: got %s", r.FormValue("grant_type"))
		}
		if r.FormValue("redirect_uri") != RedirectURI {
			t.Errorf("redirect_uri 不匹配: got %s", r.FormValue("redirect_uri"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "new-access-token",
			ExpiresIn:    3600,
			TokenType:    "Bearer",
			Scope:        "openid email",
			RefreshToken: "new-refresh-token",
		})
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		TokenURL: server.URL,
	})

	tokenResp, err := client.ExchangeCode(context.Background(), "test-auth-code", "test-verifier")
	if err != nil {
		t.Fatalf("ExchangeCode 失败: %v", err)
	}
	if tokenResp.AccessToken != "new-access-token" {
		t.Errorf("AccessToken 不匹配: got %s, want new-access-token", tokenResp.AccessToken)
	}
	if tokenResp.RefreshToken != "new-refresh-token" {
		t.Errorf("RefreshToken 不匹配: got %s, want new-refresh-token", tokenResp.RefreshToken)
	}
	if tokenResp.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn 不匹配: got %d, want 3600", tokenResp.ExpiresIn)
	}
	if tokenResp.TokenType != "Bearer" {
		t.Errorf("TokenType 不匹配: got %s, want Bearer", tokenResp.TokenType)
	}
	if tokenResp.Scope != "openid email" {
		t.Errorf("Scope 不匹配: got %s, want openid email", tokenResp.Scope)
	}
}

// TestClient_ExchangeCode_ServerError_RealCall invokes code exchange against a
// redirected token failure. The returned error must expose the asserted
// upstream failure information after the client consumes the response.
func TestClient_ExchangeCode_ServerError_RealCall(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		TokenURL: server.URL,
	})

	_, err := client.ExchangeCode(context.Background(), "expired-code", "verifier")
	if err == nil {
		t.Fatal("服务器返回 400 时应返回错误")
	}
	if !strings.Contains(err.Error(), "token exchange failed") {
		t.Errorf("错误信息应包含 'token exchange failed': got %s", err.Error())
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("错误信息应包含状态码 400: got %s", err.Error())
	}
}

// TestClient_ExchangeCode_InvalidJSON_RealCall returns malformed token JSON to
// Client.ExchangeCode through the fixture transport. The client must reject
// decoding rather than treating the successful status alone as a valid result.
func TestClient_ExchangeCode_InvalidJSON_RealCall(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		TokenURL: server.URL,
	})

	_, err := client.ExchangeCode(context.Background(), "code", "verifier")
	if err == nil {
		t.Fatal("无效 JSON 响应应返回错误")
	}
	if !strings.Contains(err.Error(), "parse token response") {
		t.Errorf("错误信息应包含 'parse token response': got %s", err.Error())
	}
}

// TestClient_ExchangeCode_ContextCanceled_RealCall cancels context before
// invoking code exchange through the fixture client. The operation must return
// failure for that context, without asserting cancellation occurred after
// server dispatch.
func TestClient_ExchangeCode_ContextCanceled_RealCall(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay a request if it unexpectedly reaches this handler; caller
		// cancellation happens before invocation, so the assertion checks the
		// returned error rather than cancellation of an observed active response.
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		TokenURL: server.URL,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.ExchangeCode(ctx, "code", "verifier")
	if err == nil {
		t.Fatal("context 取消时应返回错误")
	}
}

// TestClient_RefreshToken_Success_RealCall invokes Client.RefreshToken through
// a redirecting transport. The server checks refresh-form authentication and
// inputs, while returned token fields must match the supplier fixture.
func TestClient_RefreshToken_Success_RealCall(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("请求方法不匹配: got %s, want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("解析表单失败: %v", err)
		}
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("grant_type 不匹配: got %s", r.FormValue("grant_type"))
		}
		if r.FormValue("refresh_token") != "my-refresh-token" {
			t.Errorf("refresh_token 不匹配: got %s", r.FormValue("refresh_token"))
		}
		if r.FormValue("client_id") != ClientID {
			t.Errorf("client_id 不匹配: got %s", r.FormValue("client_id"))
		}
		if r.FormValue("client_secret") != "test-secret" {
			t.Errorf("client_secret 不匹配: got %s", r.FormValue("client_secret"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken: "refreshed-access-token",
			ExpiresIn:   3600,
			TokenType:   "Bearer",
		})
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		TokenURL: server.URL,
	})

	tokenResp, err := client.RefreshToken(context.Background(), "my-refresh-token")
	if err != nil {
		t.Fatalf("RefreshToken 失败: %v", err)
	}
	if tokenResp.AccessToken != "refreshed-access-token" {
		t.Errorf("AccessToken 不匹配: got %s, want refreshed-access-token", tokenResp.AccessToken)
	}
	if tokenResp.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn 不匹配: got %d, want 3600", tokenResp.ExpiresIn)
	}
}

// TestClient_RefreshToken_ServerError_RealCall returns a token-endpoint
// failure during Client.RefreshToken. The client must expose the asserted
// upstream error information after consuming its response rather than decoding
// successful credentials.
func TestClient_RefreshToken_ServerError_RealCall(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"token revoked"}`))
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		TokenURL: server.URL,
	})

	_, err := client.RefreshToken(context.Background(), "revoked-token")
	if err == nil {
		t.Fatal("服务器返回 401 时应返回错误")
	}
	if !strings.Contains(err.Error(), "token refresh failed") {
		t.Errorf("错误信息应包含 'token refresh failed': got %s", err.Error())
	}
}

// TestClient_RefreshToken_InvalidJSON_RealCall returns malformed JSON with the
// refresh fixture successful status. Client.RefreshToken must fail decoding so
// status alone cannot supply usable token observations.
func TestClient_RefreshToken_InvalidJSON_RealCall(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		TokenURL: server.URL,
	})

	_, err := client.RefreshToken(context.Background(), "refresh-tok")
	if err == nil {
		t.Fatal("无效 JSON 响应应返回错误")
	}
	if !strings.Contains(err.Error(), "parse token response") {
		t.Errorf("错误信息应包含 'parse token response': got %s", err.Error())
	}
}

// TestClient_RefreshToken_ContextCanceled_RealCall cancels context before
// invoking refresh through the fixture client. The operation must fail for
// that context without requiring the delayed server handler to receive a
// request.
func TestClient_RefreshToken_ContextCanceled_RealCall(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "test-secret"
	t.Cleanup(func() { defaultClientSecret = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		TokenURL: server.URL,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.RefreshToken(ctx, "refresh-tok")
	if err == nil {
		t.Fatal("context 取消时应返回错误")
	}
}

// TestClient_GetUserInfo_Success_RealCall invokes Client.GetUserInfo against a
// redirected local endpoint. Authentication and returned account identity must
// match the fixture through the actual client request path.
func TestClient_GetUserInfo_Success_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("请求方法不匹配: got %s, want GET", r.Method)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer user-access-token" {
			t.Errorf("Authorization 不匹配: got %s", auth)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(UserInfo{
			Email:      "test@example.com",
			Name:       "Test User",
			GivenName:  "Test",
			FamilyName: "User",
			Picture:    "https://example.com/avatar.jpg",
		})
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		UserInfoURL: server.URL,
	})

	userInfo, err := client.GetUserInfo(context.Background(), "user-access-token")
	if err != nil {
		t.Fatalf("GetUserInfo 失败: %v", err)
	}
	if userInfo.Email != "test@example.com" {
		t.Errorf("Email 不匹配: got %s, want test@example.com", userInfo.Email)
	}
	if userInfo.Name != "Test User" {
		t.Errorf("Name 不匹配: got %s, want Test User", userInfo.Name)
	}
	if userInfo.GivenName != "Test" {
		t.Errorf("GivenName 不匹配: got %s, want Test", userInfo.GivenName)
	}
	if userInfo.FamilyName != "User" {
		t.Errorf("FamilyName 不匹配: got %s, want User", userInfo.FamilyName)
	}
	if userInfo.Picture != "https://example.com/avatar.jpg" {
		t.Errorf("Picture 不匹配: got %s", userInfo.Picture)
	}
}

// TestClient_GetUserInfo_Unauthorized_RealCall returns an unauthorized
// response to the actual user-info client method. The result must fail with
// the asserted upstream status information rather than returning decoded
// account identity.
func TestClient_GetUserInfo_Unauthorized_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		UserInfoURL: server.URL,
	})

	_, err := client.GetUserInfo(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("服务器返回 401 时应返回错误")
	}
	if !strings.Contains(err.Error(), "fetch user info failed") {
		t.Errorf("错误信息应包含 'fetch user info failed': got %s", err.Error())
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("错误信息应包含状态码 401: got %s", err.Error())
	}
}

// TestClient_GetUserInfo_InvalidJSON_RealCall returns malformed user-info JSON
// with a successful fixture status. Client.GetUserInfo must reject decoding
// instead of treating the HTTP status as sufficient account data.
func TestClient_GetUserInfo_InvalidJSON_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{broken`))
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		UserInfoURL: server.URL,
	})

	_, err := client.GetUserInfo(context.Background(), "token")
	if err == nil {
		t.Fatal("无效 JSON 响应应返回错误")
	}
	if !strings.Contains(err.Error(), "parse user info") {
		t.Errorf("错误信息应包含 'parse user info': got %s", err.Error())
	}
}

// TestClient_GetUserInfo_ContextCanceled_RealCall cancels the supplied context
// before invoking user-info lookup. The client must return failure without
// this fixture establishing cancellation after the server begins processing.
func TestClient_GetUserInfo_ContextCanceled_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClientWithRedirect(map[string]string{
		UserInfoURL: server.URL,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetUserInfo(ctx, "token")
	if err == nil {
		t.Fatal("context 取消时应返回错误")
	}
}

// withMockBaseURLs replaces package endpoint values until t cleanup restores
// both globals. The supplied slice is borrowed, and a nonempty list also sets
// BaseURL to its first entry.
//
// An empty list leaves the previous BaseURL while replacing BaseURLs.
// Restoration captures both previous values before assignment, so later tests
// observe the earlier configuration after cleanup. These globals are not
// locked together; fixtures using this helper run serially and keep the
// supplied slice stable through all requests that consult it.
func withMockBaseURLs(t *testing.T, urls []string) {
	t.Helper()
	// Capture both prior globals before replacement and restore them together
	// during cleanup, so subsequent serialized fixtures receive the earlier
	// account and default-route configuration.
	origBaseURLs := BaseURLs
	origBaseURL := BaseURL
	BaseURLs = urls
	if len(urls) > 0 {
		BaseURL = urls[0]
	}
	t.Cleanup(func() {
		BaseURLs = origBaseURLs
		BaseURL = origBaseURL
	})
}

// TestClient_LoadCodeAssist_Success_RealCall invokes account discovery against
// a configured local endpoint. The request must include IDE/authentication
// metadata, and typed plus raw results must retain the asserted account
// observations.
func TestClient_LoadCodeAssist_Success_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("请求方法不匹配: got %s, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1internal:loadCodeAssist") {
			t.Errorf("URL 路径不匹配: got %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("Authorization 不匹配: got %s", auth)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type 不匹配: got %s", ct)
		}
		if ua := r.Header.Get("User-Agent"); ua != GetUserAgentForContext(context.Background()) {
			t.Errorf("User-Agent 不匹配: got %s", ua)
		}

		var reqBody LoadCodeAssistRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("解析请求体失败: %v", err)
		}
		if reqBody.Metadata.IDEType != "ANTIGRAVITY" {
			t.Errorf("IDEType 不匹配: got %s, want ANTIGRAVITY", reqBody.Metadata.IDEType)
		}
		if strings.TrimSpace(reqBody.Metadata.IDEVersion) == "" {
			t.Errorf("IDEVersion 不应为空")
		}
		if reqBody.Metadata.IDEName != "antigravity" {
			t.Errorf("IDEName 不匹配: got %s, want antigravity", reqBody.Metadata.IDEName)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"cloudaicompanionProject": "test-project-123",
			"currentTier": {"id": "free-tier", "name": "Free"},
			"paidTier": {"id": "g1-pro-tier", "name": "Pro", "description": "Pro plan"}
		}`))
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	resp, rawResp, err := client.LoadCodeAssist(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("LoadCodeAssist 失败: %v", err)
	}
	if resp.CloudAICompanionProject != "test-project-123" {
		t.Errorf("CloudAICompanionProject 不匹配: got %s", resp.CloudAICompanionProject)
	}
	if resp.GetTier() != "g1-pro-tier" {
		t.Errorf("GetTier 不匹配: got %s, want g1-pro-tier", resp.GetTier())
	}
	if resp.CurrentTier == nil || resp.CurrentTier.ID != "free-tier" {
		t.Errorf("CurrentTier 不匹配: got %+v", resp.CurrentTier)
	}
	if resp.PaidTier == nil || resp.PaidTier.ID != "g1-pro-tier" {
		t.Errorf("PaidTier 不匹配: got %+v", resp.PaidTier)
	}
	if rawResp == nil {
		t.Fatal("rawResp 不应为 nil")
	}
	if rawResp["cloudaicompanionProject"] != "test-project-123" {
		t.Errorf("rawResp cloudaicompanionProject 不匹配: got %v", rawResp["cloudaicompanionProject"])
	}
}

// TestClient_LoadCodeAssist_HTTPError_RealCall returns an account-discovery
// HTTP failure through the actual client method. The operation must fail with
// the asserted response status rather than returning usable project data.
func TestClient_LoadCodeAssist_HTTPError_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	_, _, err := client.LoadCodeAssist(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("服务器返回 403 时应返回错误")
	}
	if !strings.Contains(err.Error(), "loadCodeAssist failed") {
		t.Errorf("错误信息应包含 'loadCodeAssist failed': got %s", err.Error())
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("错误信息应包含状态码 403: got %s", err.Error())
	}
}

// TestClient_LoadCodeAssist_InvalidJSON_RealCall returns malformed discovery
// JSON with successful status at one configured endpoint. The returned failure
// must identify response parsing; this fixture does not observe endpoint
// advancement.
func TestClient_LoadCodeAssist_InvalidJSON_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not valid json!!!`))
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	_, _, err := client.LoadCodeAssist(context.Background(), "token")
	if err == nil {
		t.Fatal("无效 JSON 响应应返回错误")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("错误信息应包含 'parse response': got %s", err.Error())
	}
}

// TestClient_LoadCodeAssist_URLFallback_RealCall returns a server failure at
// the first configured discovery endpoint. The second endpoint must be called
// and supply usable account data through the actual client method.
func TestClient_LoadCodeAssist_URLFallback_RealCall(t *testing.T) {
	// Return an eligible server failure from the first discovery endpoint so
	// configured-order recovery reaches the second fixture and its usable account
	// data.
	callCount := 0
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"cloudaicompanionProject": "fallback-project",
			"currentTier": {"id": "free-tier", "name": "Free"}
		}`))
	}))
	defer server2.Close()

	withMockBaseURLs(t, []string{server1.URL, server2.URL})

	client := mustNewClient(t, "")
	resp, _, err := client.LoadCodeAssist(context.Background(), "token")
	if err != nil {
		t.Fatalf("LoadCodeAssist 应在 fallback 后成功: %v", err)
	}
	if resp.CloudAICompanionProject != "fallback-project" {
		t.Errorf("CloudAICompanionProject 不匹配: got %s", resp.CloudAICompanionProject)
	}
	if callCount != 2 {
		t.Errorf("应该调用了 2 个 server，实际调用 %d 次", callCount)
	}
}

// TestClient_LoadCodeAssist_AllURLsFail_RealCall configures two discovery
// endpoints with eligible failure statuses. The actual method must return an
// error for those fixtures rather than usable account observations.
func TestClient_LoadCodeAssist_AllURLsFail_RealCall(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"bad_gateway"}`))
	}))
	defer server2.Close()

	withMockBaseURLs(t, []string{server1.URL, server2.URL})

	client := mustNewClient(t, "")
	_, _, err := client.LoadCodeAssist(context.Background(), "token")
	if err == nil {
		t.Fatal("所有 URL 都失败时应返回错误")
	}
}

// TestClient_LoadCodeAssist_ContextCanceled_RealCall cancels context before
// account discovery against a local fixture. The actual method must fail for
// cancellation without the test proving a request reached the server first.
func TestClient_LoadCodeAssist_ContextCanceled_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := client.LoadCodeAssist(ctx, "token")
	if err == nil {
		t.Fatal("context 取消时应返回错误")
	}
}

// TestClient_FetchAvailableModels_Success_RealCall queries fixture model and
// quota data with a positive body limit. The actual client must retain
// asserted typed/raw model observations and request metadata.
func TestClient_FetchAvailableModels_Success_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("请求方法不匹配: got %s, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1internal:fetchAvailableModels") {
			t.Errorf("URL 路径不匹配: got %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("Authorization 不匹配: got %s", auth)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type 不匹配: got %s", ct)
		}
		if ua := r.Header.Get("User-Agent"); ua != GetUserAgentForContext(context.Background()) {
			t.Errorf("User-Agent 不匹配: got %s", ua)
		}

		var reqBody FetchAvailableModelsRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("解析请求体失败: %v", err)
		}
		if reqBody.Project != "project-abc" {
			t.Errorf("Project 不匹配: got %s, want project-abc", reqBody.Project)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"models": {
				"gemini-2.0-flash": {
					"quotaInfo": {
						"remainingFraction": 0.85,
						"resetTime": "2025-01-01T00:00:00Z"
					}
				},
				"gemini-2.5-pro": {
					"quotaInfo": {
						"remainingFraction": 0.5
					}
				}
			}
		}`))
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	resp, rawResp, err := client.FetchAvailableModels(context.Background(), "test-token", "project-abc", defaultFetchAvailableModelsBodyLimit)
	if err != nil {
		t.Fatalf("FetchAvailableModels 失败: %v", err)
	}
	if resp.Models == nil {
		t.Fatal("Models 不应为 nil")
	}
	if len(resp.Models) != 2 {
		t.Errorf("Models 数量不匹配: got %d, want 2", len(resp.Models))
	}

	flashModel, ok := resp.Models["gemini-2.0-flash"]
	if !ok {
		t.Fatal("缺少 gemini-2.0-flash 模型")
	}
	if flashModel.QuotaInfo == nil {
		t.Fatal("gemini-2.0-flash QuotaInfo 不应为 nil")
	}
	if flashModel.QuotaInfo.RemainingFraction != 0.85 {
		t.Errorf("RemainingFraction 不匹配: got %f, want 0.85", flashModel.QuotaInfo.RemainingFraction)
	}
	if flashModel.QuotaInfo.ResetTime != "2025-01-01T00:00:00Z" {
		t.Errorf("ResetTime 不匹配: got %s", flashModel.QuotaInfo.ResetTime)
	}

	proModel, ok := resp.Models["gemini-2.5-pro"]
	if !ok {
		t.Fatal("缺少 gemini-2.5-pro 模型")
	}
	if proModel.QuotaInfo == nil {
		t.Fatal("gemini-2.5-pro QuotaInfo 不应为 nil")
	}
	if proModel.QuotaInfo.RemainingFraction != 0.5 {
		t.Errorf("RemainingFraction 不匹配: got %f, want 0.5", proModel.QuotaInfo.RemainingFraction)
	}

	if rawResp == nil {
		t.Fatal("rawResp 不应为 nil")
	}
	if rawResp["models"] == nil {
		t.Error("rawResp models 不应为 nil")
	}
}

// TestClient_FetchAvailableModels_RejectsNonPositiveBodyLimit passes zero
// to model discovery with the ordinary constructed client. The returned
// error must name the positive-limit requirement asserted by this fixture.
func TestClient_FetchAvailableModels_RejectsNonPositiveBodyLimit(t *testing.T) {
	client := mustNewClient(t, "")
	_, _, err := client.FetchAvailableModels(context.Background(), "token", "proj", 0)
	if err == nil || !strings.Contains(err.Error(), "body limit must be positive") {
		t.Fatalf("非正读取上限应返回调用错误: %v", err)
	}
}

// TestClient_FetchAvailableModels_UsesConfiguredBodyLimit returns a body
// beyond the supplied model-query allowance. The client must report oversize
// failure after bounded capture instead of decoding a truncated successful
// response.
func TestClient_FetchAvailableModels_UsesConfiguredBodyLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":{"model-a":{}}}`))
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	_, _, err := client.FetchAvailableModels(context.Background(), "token", "proj", 8)
	if err == nil {
		t.Fatal("响应超过配置上限时应返回错误")
	}
	if !strings.Contains(err.Error(), "response exceeds 8 bytes") {
		t.Fatalf("错误应包含配置上限: %v", err)
	}
}

// TestClient_FetchAvailableModels_HTTPError_RealCall returns a model-query
// HTTP failure through the actual client method. The operation must fail with
// asserted response information rather than returning model/quota
// observations.
func TestClient_FetchAvailableModels_HTTPError_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	_, _, err := client.FetchAvailableModels(context.Background(), "bad-token", "proj", defaultFetchAvailableModelsBodyLimit)
	if err == nil {
		t.Fatal("服务器返回 403 时应返回错误")
	}
	if !strings.Contains(err.Error(), "fetchAvailableModels failed") {
		t.Errorf("错误信息应包含 'fetchAvailableModels failed': got %s", err.Error())
	}
}

// TestClient_FetchAvailableModels_InvalidJSON_RealCall returns malformed model
// JSON with successful status inside the supplied bound. The client must fail
// decoding rather than accepting that status as usable model data.
func TestClient_FetchAvailableModels_InvalidJSON_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<<<not json>>>`))
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	_, _, err := client.FetchAvailableModels(context.Background(), "token", "proj", defaultFetchAvailableModelsBodyLimit)
	if err == nil {
		t.Fatal("无效 JSON 响应应返回错误")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("错误信息应包含 'parse response': got %s", err.Error())
	}
}

// TestClient_FetchAvailableModels_URLFallback_RealCall returns rate limiting
// at the first configured model endpoint. The next endpoint must supply the
// successful model fixture through account-model fallback handling.
func TestClient_FetchAvailableModels_URLFallback_RealCall(t *testing.T) {
	callCount := 0
	// Return rate limiting from the first model endpoint so configured-order
	// recovery reaches the second fixture after capturing and closing the failed
	// body.
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models": {"model-a": {}}}`))
	}))
	defer server2.Close()

	withMockBaseURLs(t, []string{server1.URL, server2.URL})

	client := mustNewClient(t, "")
	resp, _, err := client.FetchAvailableModels(context.Background(), "token", "proj", defaultFetchAvailableModelsBodyLimit)
	if err != nil {
		t.Fatalf("FetchAvailableModels 应在 fallback 后成功: %v", err)
	}
	if _, ok := resp.Models["model-a"]; !ok {
		t.Error("应返回 fallback server 的模型")
	}
	if callCount != 2 {
		t.Errorf("应该调用了 2 个 server，实际调用 %d 次", callCount)
	}
}

// TestClient_FetchAvailableModels_AllURLsFail_RealCall configures two model
// endpoints with eligible failure statuses. The actual bounded query must
// return an error for those fixtures rather than usable model data.
func TestClient_FetchAvailableModels_AllURLsFail_RealCall(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`not found`))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer server2.Close()

	withMockBaseURLs(t, []string{server1.URL, server2.URL})

	client := mustNewClient(t, "")
	_, _, err := client.FetchAvailableModels(context.Background(), "token", "proj", defaultFetchAvailableModelsBodyLimit)
	if err == nil {
		t.Fatal("所有 URL 都失败时应返回错误")
	}
}

// TestClient_FetchAvailableModels_ContextCanceled_RealCall cancels context
// before the bounded model query begins. The actual client must fail without
// this fixture establishing cancellation after a dispatched server response.
func TestClient_FetchAvailableModels_ContextCanceled_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := client.FetchAvailableModels(ctx, "token", "proj", defaultFetchAvailableModelsBodyLimit)
	if err == nil {
		t.Fatal("context 取消时应返回错误")
	}
}

// TestClient_FetchAvailableModels_EmptyModels_RealCall returns a valid
// account-model response containing an empty model map. The actual client must
// accept a nonnil empty Models map and return nonnil raw decoded storage.
// Their presence and size are asserted by this fixture.
func TestClient_FetchAvailableModels_EmptyModels_RealCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models": {}}`))
	}))
	defer server.Close()

	withMockBaseURLs(t, []string{server.URL})

	client := mustNewClient(t, "")
	resp, rawResp, err := client.FetchAvailableModels(context.Background(), "token", "proj", defaultFetchAvailableModelsBodyLimit)
	if err != nil {
		t.Fatalf("FetchAvailableModels 失败: %v", err)
	}
	if resp.Models == nil {
		t.Fatal("Models 不应为 nil")
	}
	if len(resp.Models) != 0 {
		t.Errorf("Models 应为空: got %d", len(resp.Models))
	}
	if rawResp == nil {
		t.Fatal("rawResp 不应为 nil")
	}
}

// Both account and model queries retry another endpoint after HTTP 408.

// TestClient_LoadCodeAssist_408Fallback_RealCall returns HTTP 408 from the
// first discovery endpoint. The actual client must advance to the second
// configured destination and recover the successful account fixture.
func TestClient_LoadCodeAssist_408Fallback_RealCall(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestTimeout)
		_, _ = w.Write([]byte(`timeout`))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cloudaicompanionProject":"p2","currentTier":"free-tier"}`))
	}))
	defer server2.Close()

	withMockBaseURLs(t, []string{server1.URL, server2.URL})

	client := mustNewClient(t, "")
	resp, _, err := client.LoadCodeAssist(context.Background(), "token")
	if err != nil {
		t.Fatalf("LoadCodeAssist 应在 408 fallback 后成功: %v", err)
	}
	if resp.CloudAICompanionProject != "p2" {
		t.Errorf("CloudAICompanionProject 不匹配: got %s", resp.CloudAICompanionProject)
	}
}

// TestClient_FetchAvailableModels_404Fallback_RealCall returns HTTP 404 from
// the first model-query endpoint. The actual bounded query must advance to the
// second configured destination and recover its model fixture.
func TestClient_FetchAvailableModels_404Fallback_RealCall(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`not found`))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":{"m1":{"quotaInfo":{"remainingFraction":1.0}}}}`))
	}))
	defer server2.Close()

	withMockBaseURLs(t, []string{server1.URL, server2.URL})

	client := mustNewClient(t, "")
	resp, _, err := client.FetchAvailableModels(context.Background(), "token", "proj", defaultFetchAvailableModelsBodyLimit)
	if err != nil {
		t.Fatalf("FetchAvailableModels 应在 404 fallback 后成功: %v", err)
	}
	if _, ok := resp.Models["m1"]; !ok {
		t.Error("应返回 fallback server 的模型 m1")
	}
}

// TestExtractProjectIDFromOnboardResponse compares absent, string, object,
// whitespace, and invalid project representations. Extraction must retain the
// trimmed valid project fixtures and return empty for unsupported supplier
// shapes.
func TestExtractProjectIDFromOnboardResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string         // human-readable project representation case
		resp map[string]any // input supplier onboarding observations
		want string         // expected extracted project identity
	}{
		{
			name: "nil response",
			resp: nil,
			want: "",
		},
		{
			name: "empty response",
			resp: map[string]any{},
			want: "",
		},
		{
			name: "project as string",
			resp: map[string]any{
				"cloudaicompanionProject": "my-project-123",
			},
			want: "my-project-123",
		},
		{
			name: "project as string with spaces",
			resp: map[string]any{
				"cloudaicompanionProject": "  my-project-123  ",
			},
			want: "my-project-123",
		},
		{
			name: "project as map with id",
			resp: map[string]any{
				"cloudaicompanionProject": map[string]any{
					"id": "proj-from-map",
				},
			},
			want: "proj-from-map",
		},
		{
			name: "project as map without id",
			resp: map[string]any{
				"cloudaicompanionProject": map[string]any{
					"name": "some-name",
				},
			},
			want: "",
		},
		{
			name: "missing cloudaicompanionProject key",
			resp: map[string]any{
				"otherField": "value",
			},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := extractProjectIDFromOnboardResponse(tc.resp)
			if got != tc.want {
				t.Fatalf("extractProjectIDFromOnboardResponse() = %q, want %q", got, tc.want)
			}
		})
	}
}
