package qoder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/util/logredact"
)

// GenerationPath 是 Qoder LLM 推理的 SSE 流式端点。
const GenerationPath = "/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"

// Client 是支持 COSY 协议的 Qoder API HTTP 客户端。
type Client struct {
	APIBaseURL    string
	ClientVersion string
	Site          Site
	MachineOS     string
	ClientIP      string
	HTTPClient    *http.Client
}

// RequestDoer 执行已构造好的 HTTP 请求。
type RequestDoer func(req *http.Request) (*http.Response, error)

// NewClient 创建新的 Qoder API 客户端。
func NewClient(apiBaseURL string) *Client {
	profile := MustProfileForSite(SiteGlobal)
	if strings.TrimSpace(apiBaseURL) != "" {
		profile.GatewayBaseURL = strings.TrimRight(strings.TrimSpace(apiBaseURL), "/")
	}
	return NewClientForProfile(profile)
}

// NewClientForSite 使用指定站点的默认 profile 创建 COSY 客户端。
func NewClientForSite(site Site) (*Client, error) {
	profile, err := ProfileForSite(site)
	if err != nil {
		return nil, err
	}
	return NewClientForProfile(profile), nil
}

// NewClientForProfile 使用可注入端点的站点 profile 创建 COSY 客户端。
func NewClientForProfile(profile Profile) *Client {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		normalized = MustProfileForSite(SiteGlobal)
	}
	return &Client{
		APIBaseURL:    strings.TrimRight(normalized.GatewayBaseURL, "/"),
		ClientVersion: normalized.ClientVersion,
		Site:          normalized.Site,
		MachineOS:     MachineOS(),
		ClientIP:      MachineIP(),
		HTTPClient:    &http.Client{},
	}
}

// StreamRequest 向 Qoder API 发送流式 POST 请求并返回响应。
func (c *Client) StreamRequest(session *SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string) (*http.Response, error) {
	return c.StreamRequestContext(context.Background(), session, path, bodyJSON, extraHeaders)
}

// StreamRequestContext 使用传入 context 向 Qoder API 发送流式 POST 请求。
func (c *Client) StreamRequestContext(ctx context.Context, session *SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string) (*http.Response, error) {
	return c.StreamRequestContextWithDoer(ctx, session, path, bodyJSON, extraHeaders, nil)
}

// StreamRequestContextWithDoer 使用传入执行器发送流式 POST 请求。
func (c *Client) StreamRequestContextWithDoer(ctx context.Context, session *SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string, doer RequestDoer) (*http.Response, error) {
	if path == "" {
		path = GenerationPath
	}

	fullURL := c.APIBaseURL + path
	encodedBody := Encode(bodyJSON)

	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, strings.NewReader(encodedBody))
	if err != nil {
		return nil, fmt.Errorf("qoder: create request: %w", err)
	}

	c.setHeaders(req, session, path, encodedBody)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	if doer == nil {
		httpClient := c.HTTPClient
		if httpClient == nil {
			httpClient = http.DefaultClient
		}
		doer = httpClient.Do
	}
	resp, err := doer(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: request failed: %w", err)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		apiErr := ParseAPIErrorBody(resp.StatusCode, string(body))
		if apiErr == nil {
			apiErr = &APIError{}
		}
		apiErr.StatusCode = resp.StatusCode
		apiErr.Body = string(body)
		if strings.TrimSpace(apiErr.Message) == "" {
			apiErr.Message = fmt.Sprintf("Qoder upstream returned HTTP %d", resp.StatusCode)
		}
		redactQoderAPIError(apiErr)
		return nil, apiErr
	}

	return resp, nil
}

// JSONRequestContextWithDoer 向 Gateway 发送签名的 COSY JSON 请求并解析响应。
// logicalPath 不包含 /algo 前缀，构造实际 URL 时会自动补齐该前缀。
func (c *Client) JSONRequestContextWithDoer(
	ctx context.Context,
	method string,
	session *SessionContext,
	logicalPath string,
	bodyJSON []byte,
	extraHeaders map[string]string,
	doer RequestDoer,
	out any,
) error {
	return c.jsonRequestContextWithDoer(ctx, method, session, logicalPath, bodyJSON, extraHeaders, doer, out, false)
}

// SignatureJSONRequestContextWithDoer 使用登录前的 Appcode 签名模式发送 Gateway JSON 请求。
func (c *Client) SignatureJSONRequestContextWithDoer(
	ctx context.Context,
	method string,
	session *SessionContext,
	logicalPath string,
	bodyJSON []byte,
	extraHeaders map[string]string,
	doer RequestDoer,
	out any,
) error {
	return c.jsonRequestContextWithDoer(ctx, method, session, logicalPath, bodyJSON, extraHeaders, doer, out, true)
}

func (c *Client) jsonRequestContextWithDoer(
	ctx context.Context,
	method string,
	session *SessionContext,
	logicalPath string,
	bodyJSON []byte,
	extraHeaders map[string]string,
	doer RequestDoer,
	out any,
	signatureOnly bool,
) error {
	if c == nil {
		return fmt.Errorf("qoder: client is nil")
	}
	if session == nil || session.Machine == nil || (!signatureOnly && session.Identity == nil) {
		return fmt.Errorf("qoder: COSY session is incomplete")
	}
	logicalPath = strings.TrimSpace(logicalPath)
	if logicalPath == "" {
		return fmt.Errorf("qoder: gateway path is required")
	}
	actualPath := logicalPath
	if !strings.HasPrefix(actualPath, "/algo/") && actualPath != "/algo" {
		actualPath = "/algo" + ensureLeadingSlash(actualPath)
	}
	encodedBody := ""
	if len(bodyJSON) > 0 {
		encodedBody = Encode(bodyJSON)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var body io.Reader
	if encodedBody != "" {
		body = strings.NewReader(encodedBody)
	}
	requestURL := c.APIBaseURL + actualPath
	if encodedBody != "" {
		// 官方 Gateway builder 会用 Encode=1 标记经过 COSY 编码的请求体，签名仍只使用不含 query 的逻辑路径。
		parsedURL, parseErr := url.Parse(requestURL)
		if parseErr != nil {
			return fmt.Errorf("qoder: parse gateway request URL: %w", parseErr)
		}
		query := parsedURL.Query()
		query.Set("Encode", "1")
		parsedURL.RawQuery = query.Encode()
		requestURL = parsedURL.String()
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return fmt.Errorf("qoder: create gateway request: %w", err)
	}
	if signatureOnly {
		c.setSignatureHeaders(req, session)
	} else {
		c.setHeaders(req, session, logicalPath, encodedBody)
	}
	req.Header.Set("Accept", "application/json")
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}
	if doer == nil {
		httpClient := c.HTTPClient
		if httpClient == nil {
			httpClient = http.DefaultClient
		}
		doer = httpClient.Do
	}
	resp, err := doer(req)
	if err != nil {
		return fmt.Errorf("qoder: gateway request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("qoder: read gateway response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ParseAPIErrorBody(resp.StatusCode, string(responseBody))
	}
	decodedBody, statusCode, err := unwrapQoderJSONResponse(responseBody)
	if err != nil {
		return err
	}
	if statusCode >= http.StatusBadRequest {
		return ParseAPIErrorBody(statusCode, string(decodedBody))
	}
	if out == nil || len(strings.TrimSpace(string(decodedBody))) == 0 {
		return nil
	}
	if err := json.Unmarshal(decodedBody, out); err != nil {
		return fmt.Errorf("qoder: parse gateway response: %w", err)
	}
	return nil
}

func ensureLeadingSlash(path string) string {
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}

// unwrapQoderJSONResponse 同时兼容 Gateway wrapper、明文 JSON 和编码后的 body。
func unwrapQoderJSONResponse(body []byte) ([]byte, int, error) {
	trimmed := []byte(strings.TrimSpace(string(body)))
	if len(trimmed) == 0 {
		return nil, http.StatusOK, nil
	}
	var wrapper struct {
		Body            json.RawMessage `json:"body"`
		StatusCodeValue int             `json:"statusCodeValue"`
		StatusCode      string          `json:"statusCode"`
	}
	if json.Unmarshal(trimmed, &wrapper) == nil && len(wrapper.Body) > 0 {
		statusCode := wrapper.StatusCodeValue
		if statusCode == 0 {
			statusCode = qoderWrapperHTTPStatus(QoderSSEWrapper{StatusCode: wrapper.StatusCode})
		}
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		var bodyString string
		if json.Unmarshal(wrapper.Body, &bodyString) == nil {
			inner := []byte(strings.TrimSpace(bodyString))
			if len(inner) > 0 && !json.Valid(inner) {
				if decoded, err := Decode(bodyString); err == nil {
					inner = decoded
				}
			}
			return inner, statusCode, nil
		}
		return wrapper.Body, statusCode, nil
	}
	if json.Valid(trimmed) {
		return trimmed, http.StatusOK, nil
	}
	decoded, err := Decode(string(trimmed))
	if err != nil || !json.Valid(decoded) {
		return nil, 0, fmt.Errorf("qoder: gateway response is not valid JSON")
	}
	return decoded, http.StatusOK, nil
}

// APIError 表示 Qoder API 返回的错误。
type APIError struct {
	StatusCode          int
	Body                string
	Code                string
	Message             string
	AgentLimitResetTime int64
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.IsAgentLimit() {
		if resetAt, ok := e.AgentLimitResetAt(); ok {
			return fmt.Sprintf("Qoder agent limit reached; resets at %s", resetAt.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04:05 Asia/Shanghai"))
		}
		return "Qoder agent limit reached"
	}
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("Qoder upstream error %s: %s", e.Code, e.Message)
	}
	if e.Message != "" {
		return e.Message
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("Qoder upstream returned HTTP %d", e.StatusCode)
	}
	return "Qoder upstream error"
}

// IsAgentLimit 判断错误是否为 Qoder agent quota/rate limit。
func (e *APIError) IsAgentLimit() bool {
	return e != nil && (e.Code == "115" || e.AgentLimitResetTime > 0)
}

// IsEntitlementDenied 判断错误是否为 Qoder 模型或账号权限拒绝。
// 这类错误不是认证 token 失效，不应消耗 refresh token。
func (e *APIError) IsEntitlementDenied() bool {
	return e != nil && e.Code == "112"
}

// AgentLimitResetAt 返回解析后的 Qoder agent limit 重置时间。
func (e *APIError) AgentLimitResetAt() (time.Time, bool) {
	if e == nil || e.AgentLimitResetTime <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(e.AgentLimitResetTime), true
}

// ParseAPIErrorBody 解析 Qoder HTTP/SSE 错误响应体。
func ParseAPIErrorBody(statusCode int, body string) *APIError {
	apiErr := &APIError{
		StatusCode: statusCode,
		Body:       body,
		Message:    fmt.Sprintf("Qoder upstream returned HTTP %d", statusCode),
	}
	applyQoderErrorPayload(apiErr, []byte(body))
	redactQoderAPIError(apiErr)
	return apiErr
}

var (
	qoderBearerTokenPattern  = regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*bearer\s+|bearer\s+)([^\s"',;]+)`)
	qoderCookiePattern       = regexp.MustCompile(`(?i)\b(cookie|set-cookie)(\s*[:=]\s*)([^\r\n"]+)`)
	qoderInlineSecretPattern = regexp.MustCompile(`(?i)\b(securityOauthToken|security_oauth_token|refreshToken|refresh_token|personalToken|personal_token|cosy-key|cosyKey)(\s*[:=]\s*)([^,\s"']+)`)
	qoderJSONCodeStringRe    = regexp.MustCompile(`(?i)("code"\s*:\s*")([0-9]{1,8})(")`)
	qoderJSONCodeNumberRe    = regexp.MustCompile(`(?i)("code"\s*:\s*)([0-9]{1,8})(\b)`)
	qoderPlainCodeNumberRe   = regexp.MustCompile(`(?i)\b(code)(\s*[:=]\s*)([0-9]{1,8})\b`)
	redactedJSONCodeRe       = regexp.MustCompile(`(?i)("code"\s*:\s*)"\*\*\*"`)
	redactedPlainCodeRe      = regexp.MustCompile(`(?i)\b(code)(\s*[:=]\s*)\*\*\*`)
)

var qoderSensitiveErrorKeys = []string{
	"authorization",
	"cookie",
	"set-cookie",
	"securityOauthToken",
	"security_oauth_token",
	"refreshToken",
	"refresh_token",
	"personalToken",
	"personal_token",
	"token",
	"cosy-key",
	"cosyKey",
	"cosy_user",
	"cosy-user",
	"uid",
	"aid",
}

// RedactSensitiveText 在错误返回给客户端或写入日志/快照前脱敏 Qoder 凭据。
func RedactSensitiveText(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	redacted := redactQoderInlineSecrets(input)
	redacted = logredact.RedactText(redacted, qoderSensitiveErrorKeys...)
	redacted = redactQoderInlineSecrets(redacted)
	return restoreQoderNumericCode(input, redacted)
}

func redactQoderInlineSecrets(input string) string {
	redacted := qoderBearerTokenPattern.ReplaceAllString(input, `${1}***`)
	redacted = qoderCookiePattern.ReplaceAllString(redacted, `${1}${2}***`)
	redacted = qoderInlineSecretPattern.ReplaceAllString(redacted, `${1}${2}***`)
	return redacted
}

func restoreQoderNumericCode(original, redacted string) string {
	if match := qoderJSONCodeStringRe.FindStringSubmatch(original); len(match) == 4 {
		return redactedJSONCodeRe.ReplaceAllString(redacted, `${1}"`+match[2]+`"`)
	}
	if match := qoderJSONCodeNumberRe.FindStringSubmatch(original); len(match) == 4 {
		return redactedJSONCodeRe.ReplaceAllString(redacted, `${1}`+match[2])
	}
	if match := qoderPlainCodeNumberRe.FindStringSubmatch(original); len(match) == 4 {
		return redactedPlainCodeRe.ReplaceAllString(redacted, `${1}${2}`+match[3])
	}
	return redacted
}

func redactQoderAPIError(apiErr *APIError) {
	if apiErr == nil {
		return
	}
	apiErr.Body = RedactSensitiveText(apiErr.Body)
	apiErr.Message = RedactSensitiveText(apiErr.Message)
}

func applyQoderErrorPayload(apiErr *APIError, payload []byte) {
	if apiErr == nil || len(payload) == 0 {
		return
	}
	var body qoderErrorBody
	if err := json.Unmarshal(payload, &body); err != nil {
		return
	}
	if code := strings.TrimSpace(string(body.Code)); code != "" {
		apiErr.Code = code
	}
	if strings.TrimSpace(body.Message) != "" {
		apiErr.Message = body.Message
	}
	if body.AgentLimitResetTime > 0 {
		apiErr.AgentLimitResetTime = body.AgentLimitResetTime
	}
	if len(body.Data) > 0 {
		applyQoderErrorPayload(apiErr, body.Data)
	}
	if strings.TrimSpace(body.Message) != "" && json.Valid([]byte(body.Message)) {
		applyQoderErrorPayload(apiErr, []byte(body.Message))
	}
}

func (c *Client) setHeaders(req *http.Request, session *SessionContext, path, encodedBody string) {
	now := strconv.FormatInt(time.Now().Unix(), 10)
	pathNoAlgo := pathWithoutAlgo(path)
	clientVersion := c.requestClientVersion(session)
	payloadB64, _ := BuildPayloadB64WithVersion(session.Info, GenerateRequestID(), clientVersion)
	signature := SignQoderRequest(payloadB64, session.CosyKey, now, encodedBody, pathNoAlgo)
	site := c.setBasicHeaders(req, session)
	httpDate := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", httpDate)
	req.Header.Set("Signature", SignCenterRequest(httpDate))
	req.Header.Set("Appcode", AppCode)

	dataPolicy := "disagree"
	organizationTags := "Normal"
	if site == SiteCN {
		dataPolicy = "DISAGREE"
		organizationTags = ""
	}
	req.Header.Set("cosy-data-policy", dataPolicy)
	req.Header.Set("cosy-date", now)
	req.Header.Set("cosy-key", session.CosyKey)
	req.Header.Set("cosy-user", session.Identity.UID)
	req.Header.Set("cosy-organization-id", session.Identity.OrganizationID)
	req.Header.Set("cosy-organization-tags", organizationTags)
	if site != SiteCN {
		// 国际站继续保留当前推理路由使用的业务头。
		req.Header.Set("cosy-scene", "assistant")
		req.Header.Set("cosy-business-product", "cli")
		req.Header.Set("cosy-business-type", "agent")
	}
	req.Header.Set("Authorization", ComposeBearer(payloadB64, signature))
}

func (c *Client) setSignatureHeaders(req *http.Request, session *SessionContext) {
	c.setBasicHeaders(req, session)
	httpDate := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", httpDate)
	req.Header.Set("Signature", SignCenterRequest(httpDate))
	req.Header.Set("Appcode", AppCode)
}

func (c *Client) setBasicHeaders(req *http.Request, session *SessionContext) Site {
	mid := session.Machine.MachineID
	machineToken := strings.TrimSpace(session.Machine.MachineToken)
	machineType := strings.TrimSpace(session.Machine.MachineType)
	site := c.Site
	if session.Site != "" {
		site = session.Site
	}
	if site == SiteCN {
		// 国内客户端的 Gateway builder 固定发送空机器 token/type/code。
		machineToken = ""
		machineType = ""
	} else {
		if machineToken == "" {
			machineToken = mid
		}
		if machineType == "" {
			machineType = "5"
		}
	}
	machineOS := strings.TrimSpace(c.MachineOS)
	if machineOS == "" {
		machineOS = MachineOS()
	}
	clientIP := mid
	if site == SiteCN {
		clientIP = strings.TrimSpace(c.ClientIP)
		if clientIP == "" {
			clientIP = MachineIP()
		}
	}
	clientVersion := c.requestClientVersion(session)
	clientType := "5"
	if site == SiteCN {
		// Qoder CN 桌面构建的运行模式为 ide，官方协议将其映射为客户端类型 0。
		clientType = "0"
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Go-http-client/2.0")
	req.Header.Set("Login-Version", "v2")
	req.Header.Set("cosy-version", clientVersion)
	req.Header.Set("cosy-clienttype", clientType)
	req.Header.Set("cosy-clientip", clientIP)
	req.Header.Set("cosy-machineos", machineOS)
	req.Header.Set("cosy-machineid", mid)
	req.Header.Set("cosy-machinetype", machineType)
	req.Header.Set("cosy-machinetoken", machineToken)
	if site == SiteCN {
		req.Header.Set("cosy-machinecode", "")
	}
	return site
}

func (c *Client) requestClientVersion(session *SessionContext) string {
	clientVersion := strings.TrimSpace(c.ClientVersion)
	if clientVersion == "" && session != nil {
		clientVersion = strings.TrimSpace(session.ClientVersion)
	}
	if clientVersion == "" {
		clientVersion = GlobalClientVersion
	}
	return clientVersion
}

// SSEEvent 表示从 Qoder 流中解析出的 SSE 事件。
type SSEEvent struct {
	Type             string // text_delta、reasoning_delta、tool_call_delta、usage、error
	Text             string // text_delta 和 reasoning_delta 事件内容
	ToolCallID       string // tool_call_delta 事件 ID
	ToolCallIndex    int    // tool_call_delta 事件序号
	HasToolCallIndex bool   // Qoder 返回 tool call index 时为 true
	ToolType         string // tool_call_delta 事件类型
	ToolName         string // tool_call_delta 事件名称
	Arguments        string // tool_call_delta 事件参数，JSON 字符串
	PromptTokens     int    // usage 事件的输入 token
	CompletionTokens int    // usage 事件的输出 token
	TotalTokens      int    // usage 事件的总 token
	UsageDetails     UsageDetails
	HasUsage         bool // Qoder 返回 usage payload 时为 true
	IsDone           bool // 收到 [DONE] 信号时为 true
}

type UsageDetails struct {
	PromptTokensDetails     *PromptTokensDetails
	CompletionTokensDetails *CompletionTokensDetails
}

type PromptTokensDetails struct {
	CachedTokens    int
	CacheableTokens int
}

type CompletionTokensDetails struct {
	ReasoningTokens int
}

// QoderSSEWrapper 是 Qoder SSE 外层结构。
type QoderSSEWrapper struct {
	Body            string `json:"body"`
	StatusCode      string `json:"statusCode"`
	StatusCodeValue int    `json:"statusCodeValue"`
}

type qoderErrorBody struct {
	Code                qoderErrorCode  `json:"code"`
	Message             string          `json:"message"`
	Data                json.RawMessage `json:"data"`
	AgentLimitResetTime int64           `json:"agentLimitResetTime"`
}

type qoderErrorCode string

func (c *qoderErrorCode) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		*c = qoderErrorCode(strings.TrimSpace(v))
	case float64:
		*c = qoderErrorCode(strconv.FormatFloat(v, 'f', -1, 64))
	}
	return nil
}

// QoderSSEInner 是 Qoder SSE body 的内层结构。
type QoderSSEInner struct {
	Choices []QoderSSEChoice `json:"choices"`
	Usage   *QoderSSEUsage   `json:"usage,omitempty"`
	Event   string           `json:"event,omitempty"`
	Type    string           `json:"type,omitempty"`
	Data    json.RawMessage  `json:"data,omitempty"`
	Index   *int             `json:"index,omitempty"`
}

type QoderSSEChoice struct {
	Delta        QoderSSEDelta   `json:"delta"`
	Message      QoderSSEMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type QoderSSEDelta struct {
	Content          string             `json:"content"`
	ReasoningContent string             `json:"reasoning_content"`
	ToolCalls        []QoderSSEToolCall `json:"tool_calls"`
}

type QoderSSEMessage struct {
	Content          any                `json:"content"`
	ReasoningContent string             `json:"reasoning_content"`
	ToolCalls        []QoderSSEToolCall `json:"tool_calls"`
}

type QoderSSEToolCall struct {
	Index      *int            `json:"index,omitempty"`
	ID         string          `json:"id"`
	ToolCallID string          `json:"tool_call_id"`
	CallID     string          `json:"call_id"`
	Type       string          `json:"type"`
	Name       string          `json:"name"`
	ToolName   string          `json:"tool_name"`
	Arguments  json.RawMessage `json:"arguments"`
	Input      json.RawMessage `json:"input"`
	Parameters json.RawMessage `json:"parameters"`
	Function   struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// QoderSSEUsage 是 Qoder SSE payload 中携带的 token usage 对象。
type QoderSSEUsage struct {
	PromptTokens            int                      `json:"-"`
	CompletionTokens        int                      `json:"-"`
	TotalTokens             int                      `json:"-"`
	InputTokens             int                      `json:"-"`
	OutputTokens            int                      `json:"-"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

func (u *QoderSSEUsage) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	u.PromptTokens = qoderIntField(raw, "prompt_tokens")
	u.CompletionTokens = qoderIntField(raw, "completion_tokens")
	u.TotalTokens = qoderIntField(raw, "total_tokens")
	u.InputTokens = qoderIntField(raw, "input_tokens")
	u.OutputTokens = qoderIntField(raw, "output_tokens")
	if value, ok := raw["prompt_tokens_details"]; ok {
		body, _ := json.Marshal(value)
		details := &PromptTokensDetails{}
		_ = details.UnmarshalJSON(body)
		u.PromptTokensDetails = details
	}
	if value, ok := raw["completion_tokens_details"]; ok {
		body, _ := json.Marshal(value)
		details := &CompletionTokensDetails{}
		_ = details.UnmarshalJSON(body)
		u.CompletionTokensDetails = details
	}
	return nil
}

func (d *PromptTokensDetails) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	d.CachedTokens = qoderIntField(raw, "cached_tokens")
	d.CacheableTokens = qoderIntField(raw, "cacheable_tokens")
	return nil
}

func (d *CompletionTokensDetails) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	d.ReasoningTokens = qoderIntField(raw, "reasoning_tokens")
	return nil
}

func qoderIntField(raw map[string]any, key string) int {
	switch value := raw[key].(type) {
	case float64:
		return int(value)
	case string:
		trimmed := strings.TrimSpace(value)
		if parsed, err := strconv.Atoi(trimmed); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int(parsed)
		}
	}
	return 0
}

// ParseSSELine 解析 Qoder 流里的单行 SSE "data:"。
// 如果该行没有有效事件，则返回 nil。
func ParseSSELine(line string) ([]SSEEvent, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return nil, nil
	}
	data := strings.TrimSpace(line[5:])

	if data == "[DONE]" {
		return []SSEEvent{{IsDone: true}}, nil
	}

	var wrapper QoderSSEWrapper
	if err := json.Unmarshal([]byte(data), &wrapper); err != nil {
		return nil, fmt.Errorf("qoder: parse SSE wrapper: %w", err)
	}

	if statusCode := qoderWrapperHTTPStatus(wrapper); statusCode >= http.StatusBadRequest {
		wrapper.StatusCodeValue = statusCode
		return nil, parseWrappedAPIError(wrapper)
	}
	if wrapper.Body == "" {
		return nil, nil
	}
	if wrapper.Body == "[DONE]" {
		return []SSEEvent{{IsDone: true}}, nil
	}

	var inner QoderSSEInner
	if err := json.Unmarshal([]byte(wrapper.Body), &inner); err != nil {
		return nil, fmt.Errorf("qoder: parse SSE inner: %w", err)
	}

	events := qoderSSEEnvelopeEvents(inner)
	hasFinalMessage := false
	for _, choice := range inner.Choices {
		delta := choice.Delta

		if delta.Content != "" {
			events = append(events, SSEEvent{
				Type: "text_delta",
				Text: delta.Content,
			})
		}

		if delta.ReasoningContent != "" {
			events = append(events, SSEEvent{
				Type: "reasoning_delta",
				Text: delta.ReasoningContent,
			})
		}

		events = appendQoderToolCallEvents(events, delta.ToolCalls)

		message := choice.Message
		if text := qoderSSEContentText(message.Content); text != "" {
			events = append(events, SSEEvent{
				Type: "text_delta",
				Text: text,
			})
		}
		if message.ReasoningContent != "" {
			events = append(events, SSEEvent{
				Type: "reasoning_delta",
				Text: message.ReasoningContent,
			})
		}
		events = appendQoderToolCallEvents(events, message.ToolCalls)
		if choice.FinishReason != "" && qoderSSEMessageHasPayload(message) {
			hasFinalMessage = true
		}
	}
	if inner.Usage != nil {
		promptTokens := inner.Usage.PromptTokens
		if promptTokens == 0 {
			promptTokens = inner.Usage.InputTokens
		}
		completionTokens := inner.Usage.CompletionTokens
		if completionTokens == 0 {
			completionTokens = inner.Usage.OutputTokens
		}
		totalTokens := inner.Usage.TotalTokens
		if totalTokens == 0 {
			totalTokens = promptTokens + completionTokens
		}
		events = append(events, SSEEvent{
			Type:             "usage",
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			UsageDetails: UsageDetails{
				PromptTokensDetails:     inner.Usage.PromptTokensDetails,
				CompletionTokensDetails: inner.Usage.CompletionTokensDetails,
			},
			HasUsage: true,
		})
	}
	if hasFinalMessage {
		events = append(events, SSEEvent{IsDone: true})
	}

	return events, nil
}

func qoderWrapperHTTPStatus(wrapper QoderSSEWrapper) int {
	if wrapper.StatusCodeValue > 0 {
		return wrapper.StatusCodeValue
	}
	status := strings.TrimSpace(wrapper.StatusCode)
	if status == "" {
		return 0
	}
	if parsed, err := strconv.Atoi(status); err == nil {
		return parsed
	}
	switch strings.ToUpper(status) {
	case "BAD_REQUEST":
		return http.StatusBadRequest
	case "UNAUTHORIZED":
		return http.StatusUnauthorized
	case "FORBIDDEN":
		return http.StatusForbidden
	case "NOT_FOUND":
		return http.StatusNotFound
	case "TOO_MANY_REQUESTS":
		return http.StatusTooManyRequests
	case "INTERNAL_SERVER_ERROR":
		return http.StatusInternalServerError
	case "BAD_GATEWAY":
		return http.StatusBadGateway
	case "SERVICE_UNAVAILABLE":
		return http.StatusServiceUnavailable
	case "GATEWAY_TIMEOUT":
		return http.StatusGatewayTimeout
	default:
		return 0
	}
}

func appendQoderToolCallEvents(events []SSEEvent, toolCalls []QoderSSEToolCall) []SSEEvent {
	syntheticIndex := 0
	for _, tc := range toolCalls {
		arguments := parseQoderToolCallArguments(
			tc.Function.Arguments,
			tc.Arguments,
			tc.Input,
			tc.Parameters,
		)
		if qoderSSEToolCallIsPlaceholder(tc, arguments) {
			continue
		}
		event := SSEEvent{
			Type:       "tool_call_delta",
			ToolCallID: firstNonEmptyQoderSSEString(tc.ID, tc.ToolCallID, tc.CallID),
			ToolType:   qoderSSEToolType(tc.Type),
			ToolName:   firstNonEmptyQoderSSEString(tc.Function.Name, tc.Name, tc.ToolName),
			Arguments:  arguments,
		}
		if tc.Index != nil {
			event.ToolCallIndex = *tc.Index
			event.HasToolCallIndex = true
		} else if len(toolCalls) > 1 {
			event.ToolCallIndex = syntheticIndex
			event.HasToolCallIndex = true
		}
		events = append(events, event)
		syntheticIndex++
	}
	return events
}

func qoderSSEToolCallIsPlaceholder(tc QoderSSEToolCall, arguments string) bool {
	meaningful := firstNonEmptyQoderSSEString(tc.ID, tc.ToolCallID, tc.CallID, tc.Function.Name, tc.Name, tc.ToolName)
	if meaningful != "" || strings.TrimSpace(arguments) != "" {
		return false
	}
	return strings.TrimSpace(tc.Type) == "" || qoderSSEToolType(tc.Type) == "function"
}

func qoderSSEEnvelopeEvents(inner QoderSSEInner) []SSEEvent {
	eventType := firstNonEmptyQoderSSEString(inner.Event, inner.Type)
	if eventType == "" {
		return nil
	}
	switch eventType {
	case "content_block_delta":
		return qoderSSEContentBlockDeltaEvents(inner.Data)
	case "tool_use_start":
		return qoderSSEToolUseStartEvents(inner.Data, inner.Index)
	case "tool_use_delta", "tool_call_delta":
		return qoderSSEToolUseDeltaEvents(inner.Data, inner.Index)
	case "message_delta":
		return qoderSSEMessageDeltaEvents(inner.Data)
	case "message_stop":
		return []SSEEvent{{IsDone: true}}
	default:
		return nil
	}
}

func qoderSSEContentBlockDeltaEvents(raw json.RawMessage) []SSEEvent {
	if len(raw) == 0 {
		return nil
	}
	var delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &delta); err != nil || delta.Text == "" {
		return nil
	}
	eventType := "text_delta"
	if delta.Type == "thinking_delta" {
		eventType = "reasoning_delta"
	}
	return []SSEEvent{{Type: eventType, Text: delta.Text}}
}

func qoderSSEToolUseStartEvents(raw json.RawMessage, envelopeIndex *int) []SSEEvent {
	if len(raw) == 0 {
		return nil
	}
	var toolUse struct {
		Index      *int   `json:"index,omitempty"`
		ID         string `json:"id"`
		ToolCallID string `json:"tool_call_id"`
		CallID     string `json:"call_id"`
		Name       string `json:"name"`
		Type       string `json:"type"`
	}
	if err := json.Unmarshal(raw, &toolUse); err != nil {
		return nil
	}
	event := SSEEvent{
		Type:       "tool_call_delta",
		ToolCallID: firstNonEmptyQoderSSEString(toolUse.ID, toolUse.ToolCallID, toolUse.CallID),
		ToolType:   qoderSSEToolType(toolUse.Type),
		ToolName:   toolUse.Name,
	}
	if index := firstNonNilQoderSSEInt(toolUse.Index, envelopeIndex); index != nil {
		event.ToolCallIndex = *index
		event.HasToolCallIndex = true
	}
	return []SSEEvent{event}
}

func qoderSSEToolUseDeltaEvents(raw json.RawMessage, envelopeIndex *int) []SSEEvent {
	if len(raw) == 0 {
		return nil
	}
	var delta struct {
		Index      *int            `json:"index,omitempty"`
		ID         string          `json:"id"`
		ToolCallID string          `json:"tool_call_id"`
		CallID     string          `json:"call_id"`
		Name       string          `json:"name"`
		ToolName   string          `json:"tool_name"`
		Type       string          `json:"type"`
		Arguments  json.RawMessage `json:"arguments"`
		Input      json.RawMessage `json:"input"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &delta); err != nil {
		return nil
	}
	event := SSEEvent{
		Type:       "tool_call_delta",
		ToolCallID: firstNonEmptyQoderSSEString(delta.ID, delta.ToolCallID, delta.CallID),
		ToolType:   qoderSSEToolType(delta.Type),
		ToolName:   firstNonEmptyQoderSSEString(delta.Name, delta.ToolName),
		Arguments:  parseQoderToolCallArguments(delta.Arguments, delta.Input, delta.Parameters),
	}
	if event.ToolCallID == "" && event.ToolName == "" && strings.TrimSpace(event.Arguments) == "" && event.ToolType == "function" {
		return nil
	}
	if index := firstNonNilQoderSSEInt(delta.Index, envelopeIndex); index != nil {
		event.ToolCallIndex = *index
		event.HasToolCallIndex = true
	}
	return []SSEEvent{event}
}

func qoderSSEMessageDeltaEvents(raw json.RawMessage) []SSEEvent {
	if len(raw) == 0 {
		return nil
	}
	var delta struct {
		Usage *QoderSSEUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &delta); err != nil || delta.Usage == nil {
		return nil
	}
	promptTokens := delta.Usage.PromptTokens
	if promptTokens == 0 {
		promptTokens = delta.Usage.InputTokens
	}
	completionTokens := delta.Usage.CompletionTokens
	if completionTokens == 0 {
		completionTokens = delta.Usage.OutputTokens
	}
	totalTokens := delta.Usage.TotalTokens
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	return []SSEEvent{{
		Type:             "usage",
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		UsageDetails: UsageDetails{
			PromptTokensDetails:     delta.Usage.PromptTokensDetails,
			CompletionTokensDetails: delta.Usage.CompletionTokensDetails,
		},
		HasUsage: true,
	}}
}

func qoderSSEContentText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			if text, ok := block["text"].(string); ok && text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func qoderSSEMessageHasPayload(message QoderSSEMessage) bool {
	return message.Content != nil || message.ReasoningContent != "" || len(message.ToolCalls) > 0
}

func parseQoderToolCallArguments(values ...json.RawMessage) string {
	for _, raw := range values {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if strings.TrimSpace(s) != "" {
				return s
			}
			continue
		}
		return string(raw)
	}
	return ""
}

func firstNonEmptyQoderSSEString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonNilQoderSSEInt(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func qoderSSEToolType(value string) string {
	switch strings.TrimSpace(value) {
	case "", "tool_use", "tool_call":
		return "function"
	default:
		return value
	}
}

func parseWrappedAPIError(wrapper QoderSSEWrapper) error {
	apiErr := &APIError{
		StatusCode: wrapper.StatusCodeValue,
		Body:       wrapper.Body,
		Message:    fmt.Sprintf("Qoder upstream returned HTTP %d", wrapper.StatusCodeValue),
	}
	applyQoderErrorPayload(apiErr, []byte(wrapper.Body))
	redactQoderAPIError(apiErr)
	return apiErr
}

// StreamEvents 从响应体读取 SSE 行并返回事件 channel。
func StreamEvents(resp *http.Response) <-chan SSEEvent {
	ch := make(chan SSEEvent, 16)
	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			events, err := ParseSSELine(line)
			if err != nil {
				ch <- SSEEvent{
					Type:   "error",
					Text:   err.Error(),
					IsDone: true,
				}
				return
			}
			for _, evt := range events {
				ch <- evt
				if evt.IsDone {
					return
				}
			}
		}
	}()
	return ch
}

// ParseSSEEvent 解析单行 SSE data，并返回第一个事件。
// 这是面向单事件消费场景的 ParseSSELine 便捷封装。
func ParseSSEEvent(line string) (*SSEEvent, error) {
	events, err := ParseSSELine(line)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}
	return &events[0], nil
}

// 保留 url 包引用，避免后续调整导入时误删。
var _ = url.Parse
