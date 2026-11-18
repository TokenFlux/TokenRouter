package qoder

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

// client.go sends signed Qoder Gateway requests and normalizes wrapped JSON,
// stream events, and error evidence.
// Gateway responses can carry effective status and JSON inside an outer
// envelope, while inference streams mix choice deltas and Anthropic-like event
// shapes. Parsing converts these representations into SSEEvent values before
// protocol writers assemble calls and usage.
//
// Signing binds the encoded body to its logical path after removing /algo and
// query parameters. Site and machine metadata select headers, including CN
// status requests before COSY identity exists. Error extraction
// resolves nested vendor codes and reset times before redaction; those
// separate observations remain available to refresh and failover policy when
// credential text is hidden.

// Gateway authentication modes select header construction; GenerationPath fixes
// the encoded inference endpoint and its agent/model-result query parameters.
const (
	qoderJSONAuthCOSY      qoderJSONAuthMode = iota // encrypted COSY session headers
	qoderJSONAuthSignature                          // pre-login Appcode signature headers
	qoderJSONAuthBearer                             // security OAuth Bearer headers

	// GenerationPath is the Qoder LLM inference SSE endpoint.
	GenerationPath = "/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
)

// Error patterns redact credentials and identity fields while recognizing
// numeric vendor codes that retry and cooldown policy still needs.
var (
	// Bearer, cookie, and inline-secret patterns remove recognized credential text
	// before and after the shared redactor. Parsed vendor code and reset fields
	// remain separate retry observations.
	qoderBearerTokenPattern  = regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*bearer\s+|bearer\s+)([^\s"',;]+)`)
	qoderCookiePattern       = regexp.MustCompile(`(?i)\b(cookie|set-cookie)(\s*[:=]\s*)([^\r\n"]+)`)
	qoderInlineSecretPattern = regexp.MustCompile(`(?i)\b(securityOauthToken|security_oauth_token|refreshToken|refresh_token|personalToken|personal_token|cosy-key|cosyKey)(\s*[:=]\s*)([^,\s"']+)`)
	// Original-code patterns and redacted-code patterns recognize the accepted
	// numeric code forms before and after generic redaction, allowing
	// restoreQoderNumericCode to retain vendor failure classification.
	qoderJSONCodeStringRe  = regexp.MustCompile(`(?i)("code"\s*:\s*")([0-9]{1,8})(")`)
	qoderJSONCodeNumberRe  = regexp.MustCompile(`(?i)("code"\s*:\s*)([0-9]{1,8})(\b)`)
	qoderPlainCodeNumberRe = regexp.MustCompile(`(?i)\b(code)(\s*[:=]\s*)([0-9]{1,8})\b`)
	redactedJSONCodeRe     = regexp.MustCompile(`(?i)("code"\s*:\s*)"\*\*\*"`)
	redactedPlainCodeRe    = regexp.MustCompile(`(?i)\b(code)(\s*[:=]\s*)\*\*\*`)

	// qoderSensitiveErrorKeys extends the shared text redactor with Qoder
	// credential and account fields before error body and message logging.
	qoderSensitiveErrorKeys = []string{
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
)

// Client retains Gateway endpoint and machine metadata used to prepare signed
// HTTP requests.
type Client struct {
	APIBaseURL string // Gateway endpoint base
	// ClientVersion wins over SessionContext.ClientVersion after trimming. Empty
	// values let requestClientVersion use the session and then the global version.
	ClientVersion string // preferred client version
	// Site selects basic header defaults unless SessionContext supplies a
	// nonempty site. CN and global choose different machine and client shapes.
	Site Site // default request site
	// MachineOS overrides the runtime platform when nonblank. Empty text asks
	// setBasicHeaders to discover the current Go architecture and OS.
	MachineOS string // machine platform text
	// ClientIP overrides CN local-interface discovery when nonblank. Global
	// headers instead use the session machine ID for their client-IP field.
	ClientIP string // CN local IP
	// HTTPClient supplies transport when no request doer is provided. Nil selects
	// http.DefaultClient, and callers bound inference through their request
	// context.
	HTTPClient *http.Client // default HTTP transport
}

// RequestDoer sends a prepared HTTP request using the caller's selected
// transport.
type RequestDoer func(req *http.Request) (*http.Response, error)

// qoderJSONAuthMode selects COSY, pre-login signature, or security-token Gateway
// authentication.
type qoderJSONAuthMode uint8

// RequestError describes a rejected client parameter before any upstream exchange.
type RequestError struct {
	Code    string // stable validation identifier
	Message string // caller-facing diagnostic text
}

// APIError holds effective HTTP and vendor details used for retry decisions.
type APIError struct {
	StatusCode int // effective HTTP status
	// Body and Message are redacted by ParseAPIErrorBody and transport failure
	// construction. Direct struct callers supply their own text; Error formats
	// Message as stored for ordinary vendor failures.
	Body                string // error or envelope body
	Code                string // vendor error code
	Message             string // wire message value
	AgentLimitResetTime int64  // Unix milliseconds
}

// SSEEvent is the normalized content, identity, usage, or completion observation
// from Qoder.
type SSEEvent struct {
	Type string // normalized event type
	Text string // text or reasoning delta
	// ToolCallID, ToolCallIndex, HasToolCallIndex, and ToolName carry call identity
	// separately from ToolType. A default function type alone cannot select a
	// parallel argument destination.
	ToolCallID    string // upstream call identifier
	ToolCallIndex int    // upstream call position
	// HasToolCallIndex distinguishes a missing position from explicit index zero.
	// The accumulator considers ToolCallIndex for association when this flag is set
	// and its value is nonnegative.
	HasToolCallIndex bool   // call position present
	ToolType         string // normalized tool type
	ToolName         string // upstream function name
	Arguments        string // JSON argument text
	// PromptTokens, CompletionTokens, and TotalTokens represent the normalized
	// event counts. Usage parsing fills zero primary counts from input/output
	// aliases and derives missing totals.
	PromptTokens     int          // input tokens
	CompletionTokens int          // output tokens
	TotalTokens      int          // total tokens
	UsageDetails     UsageDetails // optional usage breakdown
	// HasUsage records payload presence independently of token counts. Explicitly
	// reported zero usage remains observable to execution and partial-result
	// accounting.
	HasUsage bool // usage payload observed
	IsDone   bool // stream completion observed
}

// UsageDetails retains optional prompt and completion details from the latest
// usage payload.
type UsageDetails struct {
	// PromptTokensDetails and CompletionTokensDetails retain objects shared by
	// copies of normalized events. Consumers keep those objects stable while
	// aggregating usage and serializing protocol responses. PromptTokensDetails
	// selects cache splitting when present. MergeQoderUsageEvent treats a nil
	// pointer as ordinary prompt input and clears cache-read counts.
	PromptTokensDetails *PromptTokensDetails // prompt usage breakdown
	// CompletionTokensDetails retains reasoning counts used by Chat usage output. A
	// nil pointer omits that breakdown, and event copies share the retained object
	// during conversion.
	CompletionTokensDetails *CompletionTokensDetails // completion usage breakdown
}

// PromptTokensDetails holds cached and cacheable token observations from Qoder
// usage.
type PromptTokensDetails struct {
	CachedTokens    int // cached input tokens
	CacheableTokens int // cacheable input tokens
}

// CompletionTokensDetails holds reasoning token observations from the completion
// usage payload.
type CompletionTokensDetails struct {
	ReasoningTokens int // completion reasoning tokens
}

// QoderSSEWrapper contains string body and effective status fields around SSE
// data.
type QoderSSEWrapper struct {
	Body string `json:"body"` // error or envelope body
	// StatusCodeValue supplies the effective status when positive. StatusCode
	// supplies a decimal code or recognized name otherwise;
	// unknown text resolves to zero for the parser's next decision.
	StatusCode      string `json:"statusCode"`      // effective HTTP status
	StatusCodeValue int    `json:"statusCodeValue"` // numeric HTTP status
}

// qoderErrorBody accepts nested vendor error metadata before error text is
// redacted.
type qoderErrorBody struct {
	Code                qoderErrorCode  `json:"code"`                // vendor error code
	Message             string          `json:"message"`             // wire message value
	Type                string          `json:"type"`                // vendor rejection classification
	Error               json.RawMessage `json:"error"`               // nested OpenAPI error object
	Data                json.RawMessage `json:"data"`                // nested event/error JSON
	AgentLimitResetTime int64           `json:"agentLimitResetTime"` // Unix milliseconds
}

// qoderErrorCode normalizes numeric and string vendor codes for retry
// classification.
type qoderErrorCode string

// QoderSSEInner represents choice, usage, and event-envelope variants of decoded
// SSE bodies.
type QoderSSEInner struct {
	Choices []QoderSSEChoice `json:"choices"` // choice content observations
	// Usage records payload presence through its pointer. ParseSSELine emits
	// HasUsage when this pointer is nonnil, including a decoded zero-count
	// observation.
	Usage *QoderSSEUsage `json:"usage,omitempty"` // optional usage payload
	// Event and Type hold alternative envelope labels. qoderSSEEnvelopeEvents
	// prefers a nonblank Event and otherwise uses Type, retaining the selected
	// spelling for exact supported-label matching.
	Event string          `json:"event,omitempty"` // preferred envelope label
	Type  string          `json:"type,omitempty"`  // alternate envelope label
	Data  json.RawMessage `json:"data,omitempty"`  // nested event/error JSON
	// Index retains pointer presence so an explicit zero position can differ from a
	// missing position. Event normalization carries this into HasToolCallIndex.
	Index *int `json:"index,omitempty"` // optional call position
}

// QoderSSEChoice retains delta and final-message representations plus completion
// evidence.
type QoderSSEChoice struct {
	Delta        QoderSSEDelta   `json:"delta"`         // incremental choice content
	Message      QoderSSEMessage `json:"message"`       // wire message value
	FinishReason string          `json:"finish_reason"` // final choice marker
}

// QoderSSEDelta contains incremental text, reasoning, and tool-call wire
// observations.
type QoderSSEDelta struct {
	Content          string             `json:"content"`           // visible message content
	ReasoningContent string             `json:"reasoning_content"` // reasoning message content
	ToolCalls        []QoderSSEToolCall `json:"tool_calls"`        // wire tool calls
}

// QoderSSEMessage contains final content and calls from a completed choice.
type QoderSSEMessage struct {
	Content          any                `json:"content"`           // visible message content
	ReasoningContent string             `json:"reasoning_content"` // reasoning message content
	ToolCalls        []QoderSSEToolCall `json:"tool_calls"`        // wire tool calls
}

// QoderSSEToolCall accepts nested and flat identity and argument wire variants.
type QoderSSEToolCall struct {
	// Index retains pointer presence so an explicit zero position can differ from a
	// missing position. Event normalization carries this into HasToolCallIndex.
	Index *int `json:"index,omitempty"` // optional call position
	// ID, ToolCallID, and CallID supply alternate invocation identifiers.
	// appendQoderToolCallEvents chooses the first nonblank field in that order
	// and retains its spelling for the accumulator.
	ID         string `json:"id"`           // preferred call identifier
	ToolCallID string `json:"tool_call_id"` // upstream call identifier
	CallID     string `json:"call_id"`      // alternate call identifier
	Type       string `json:"type"`         // wire tool type
	// Function.Name takes precedence over Name and ToolName for call naming.
	// Function.Arguments precedes Arguments, Input, and Parameters, selecting one
	// representation for the later accumulator.
	Name       string          `json:"name"`       // function name text
	ToolName   string          `json:"tool_name"`  // upstream function name
	Arguments  json.RawMessage `json:"arguments"`  // JSON argument text
	Input      json.RawMessage `json:"input"`      // alternate argument JSON
	Parameters json.RawMessage `json:"parameters"` // alternate argument JSON
	Function   struct {        // nested function payload
		Name      string          `json:"name"`      // function name text
		Arguments json.RawMessage `json:"arguments"` // JSON argument text
	} `json:"function"`
}

// QoderSSEUsage holds tolerated primary and alias token counts plus optional
// details.
type QoderSSEUsage struct {
	// PromptTokens and CompletionTokens are primary counts; InputTokens and
	// OutputTokens supply aliases when their corresponding primary count is zero.
	// Parsing derives a zero TotalTokens from the selected pair.
	PromptTokens     int `json:"-"` // input tokens
	CompletionTokens int `json:"-"` // output tokens
	TotalTokens      int `json:"-"` // total tokens
	InputTokens      int `json:"-"` // alias input tokens
	OutputTokens     int `json:"-"` // alias output tokens
	// PromptTokensDetails and CompletionTokensDetails retain objects shared by
	// copies of normalized events. Consumers keep those objects stable while
	// aggregating usage and serializing protocol responses. PromptTokensDetails
	// selects cache splitting when present. MergeQoderUsageEvent treats a nil
	// pointer as ordinary prompt input and clears cache-read counts.
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"` // prompt usage breakdown
	// CompletionTokensDetails retains reasoning counts used by Chat usage output. A
	// nil pointer omits that breakdown, and event copies share the retained object
	// during conversion.
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"` // completion usage breakdown
}

// Error returns the validation diagnostic for protocol-specific error presentation.
func (e *RequestError) Error() string { return e.Message }

// IsRequestError identifies local validation failures through wrapping layers.
func IsRequestError(err error) bool {
	_, ok := errors.AsType[*RequestError](err)
	return ok
}

// MayRefreshAttempt permits refresh for wrapped API errors with HTTP 401 or 403.
// Agent-limit and entitlement failures return false, so retry policy does not
// spend refresh tokens on quota or model-access rejection.
func MayRefreshAttempt(err error) bool {
	var failure *APIError
	if !errors.As(err, &failure) || failure.IsAgentLimit() || failure.IsEntitlementDenied() {
		return false
	}
	return failure.StatusCode == http.StatusUnauthorized || failure.StatusCode == http.StatusForbidden
}

// MaySwitchAttempt reports retryable API failures and permits unknown errors for
// the Chat path. The attempt owner controls retry count and whether downstream
// output has already closed the retry window.
func MaySwitchAttempt(err error) bool {
	return maySwitchAttempt(err, true)
}

// MaySwitchCompatibleAttempt reports API failures eligible for Messages or
// Responses provider failover. Errors that do not wrap APIError return false;
// the attempt owner still controls output commitment and retries.
func MaySwitchCompatibleAttempt(err error) bool {
	return maySwitchAttempt(err, false)
}

// maySwitchAttempt classifies agent-limit, entitlement, HTTP 429, and server
// failures for failover. The unknown argument controls errors lacking APIError,
// keeping Chat and compatible protocol retry decisions separate.
func maySwitchAttempt(err error, unknown bool) bool {
	if IsRequestError(err) {
		return false
	}
	var failure *APIError
	if !errors.As(err, &failure) {
		return unknown
	}
	return failure.IsAgentLimit() || failure.IsEntitlementDenied() || failure.StatusCode == http.StatusTooManyRequests || failure.StatusCode >= 500
}

// NewClient constructs a global Gateway client with an optional base-URL
// override. The override is trimmed and loses trailing slashes; empty input
// selects the global profile and caller contexts bound requests.
func NewClient(apiBaseURL string) *Client {
	profile := MustProfileForSite(SiteGlobal)
	if strings.TrimSpace(apiBaseURL) != "" {
		profile.GatewayBaseURL = strings.TrimRight(strings.TrimSpace(apiBaseURL), "/")
	}
	return NewClientForProfile(profile)
}

// NewClientForProfile constructs a Gateway client from normalized site endpoints
// and client metadata. An invalid site selects global defaults. The HTTP client
// has no timeout, so inference callers supply cancellation and execution
// budgets.
func NewClientForProfile(profile Profile) *Client {
	normalized, err := NormalizeProfile(profile)
	// Client construction can receive an invalid override profile. Select complete
	// global defaults so endpoint and client metadata remain a coherent request
	// shape.
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

// StreamRequestContext sends a signed streaming POST using the client transport.
// Empty path selects GenerationPath and nil context selects Background. Callers
// provide a complete session and consume or close a successful response body.
func (c *Client) StreamRequestContext(ctx context.Context, session *SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string) (*http.Response, error) {
	return c.StreamRequestContextWithDoer(ctx, session, path, bodyJSON, extraHeaders, nil)
}

// StreamRequestContextWithDoer sends a signed streaming POST through the
// selected doer. Empty path and nil context select endpoint and context
// defaults; nil doer uses HTTPClient or http.DefaultClient. Callers provide a
// complete session, extra headers override generated headers, and successful
// response bodies belong to the reader.
func (c *Client) StreamRequestContextWithDoer(ctx context.Context, session *SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string, doer RequestDoer) (*http.Response, error) {
	if path == "" {
		path = GenerationPath
	}

	fullURL := c.APIBaseURL + path
	encodedBody := Encode(bodyJSON)

	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, strings.NewReader(encodedBody))
	if err != nil {
		return nil, fmt.Errorf("qoder: create request: %w", err)
	}

	c.setHeaders(req, session, path, encodedBody)
	for name, value := range desktopRequestHeaders() {
		req.Header.Set(name, value)
	}
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

	if resp.StatusCode != http.StatusOK {
		// Bound failure text before consuming and closing the rejected response.
		// Parsing and redaction retain retry metadata while limiting diagnostic body
		// size.
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

// JSONRequestContextWithDoer sends Gateway JSON using COSY authentication and
// decodes into out. The logical path gains /algo for transport and Encode=1 for
// nonempty bodies; response consumption and closure belong to this call.
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
	return c.jsonRequestContextWithDoer(ctx, method, session, logicalPath, bodyJSON, extraHeaders, doer, out, qoderJSONAuthCOSY)
}

// SignatureJSONRequestContextWithDoer sends Gateway JSON with pre-login Appcode
// authentication. The session needs a machine and can omit identity; the shared
// request implementation owns body encoding, response decoding, and closure.
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
	return c.jsonRequestContextWithDoer(ctx, method, session, logicalPath, bodyJSON, extraHeaders, doer, out, qoderJSONAuthSignature)
}

// BearerJSONRequestContextWithDoer sends Gateway JSON using the session's
// security OAuth token. A nonempty token and machine identity are required; the
// shared request implementation owns body encoding, response decoding, and
// closure.
func (c *Client) BearerJSONRequestContextWithDoer(
	ctx context.Context,
	method string,
	session *SessionContext,
	logicalPath string,
	bodyJSON []byte,
	extraHeaders map[string]string,
	doer RequestDoer,
	out any,
) error {
	return c.jsonRequestContextWithDoer(ctx, method, session, logicalPath, bodyJSON, extraHeaders, doer, out, qoderJSONAuthBearer)
}

// jsonRequestContextWithDoer sends one encoded Gateway request and resolves its
// effective response status. Client, machine, logical path, and the selected
// authentication inputs are validated before transport. Nil context selects
// Background and nil doer selects the configured or default HTTP client; extra
// headers are applied last.
//
// Transport uses /algo and an Encode query marker, while COSY signing uses the
// original logical path. HTTP and wrapped statuses are checked before decoding
// out. The body is bounded and closed here, and empty decoded content or nil out
// permits success without JSON assignment.
func (c *Client) jsonRequestContextWithDoer(
	ctx context.Context,
	method string,
	session *SessionContext,
	logicalPath string,
	bodyJSON []byte,
	extraHeaders map[string]string,
	doer RequestDoer,
	out any,
	authMode qoderJSONAuthMode,
) error {
	if c == nil {
		return fmt.Errorf("qoder: client is nil")
	}
	if session == nil || session.Machine == nil || (authMode != qoderJSONAuthSignature && session.Identity == nil) {
		return fmt.Errorf("qoder: COSY session is incomplete")
	}
	if authMode == qoderJSONAuthBearer && strings.TrimSpace(session.Identity.SecurityOauthToken) == "" {
		return fmt.Errorf("qoder: security OAuth token is missing")
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
		// A nonempty body is COSY encoded for transport. Add Encode=1 to its URL while
		// signing the logical path independently so query markers cannot change the
		// digest.
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
	switch authMode {
	case qoderJSONAuthSignature:
		c.setSignatureHeaders(req, session)
	case qoderJSONAuthBearer:
		c.setBearerHeaders(req, session)
	default:
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
	// Read one byte beyond the metadata limit so an oversized response cannot
	// publish a valid-looking truncated prefix as a complete model or quota observation.
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return fmt.Errorf("qoder: read gateway response: %w", err)
	}
	if len(responseBody) > 4<<20 {
		return errors.New("qoder: gateway metadata response exceeds size limit")
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
	if out == nil || strings.TrimSpace(string(decodedBody)) == "" {
		return nil
	}
	if err := json.Unmarshal(decodedBody, out); err != nil {
		return fmt.Errorf("qoder: parse gateway response: %w", err)
	}
	return nil
}

// ensureLeadingSlash supplies a leading slash when the caller omitted it.
// Existing leading slashes remain as provided, and empty input becomes a slash
// for Gateway path construction.
func ensureLeadingSlash(path string) string {
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}

// desktopRequestHeaders returns independent app workload metadata for inference
// and model discovery. Legacy credential and quota exchanges keep their own
// headers; callers may edit this map without changing another request's profile.
func desktopRequestHeaders() map[string]string {
	return map[string]string{
		"Cosy-ClientType":       DesktopClientType,
		"Cosy-Business-Product": DesktopProduct,
		"Cosy-Business-Type":    "agent",
		"Cosy-Scene":            DesktopProduct,
	}
}

// unwrapQoderJSONResponse resolves plain, encoded, and enveloped Gateway
// response bodies with their status. Empty content returns HTTP 200. A wrapper
// can contain an object or a string, and its numeric status wins over the
// status-name form.
// String bodies retain their JSON when valid and otherwise attempt COSY
// decoding. Plain JSON uses HTTP 200; non-JSON outer content needs successful
// COSY decoding into valid JSON. This keeps body representation conversion
// separate from the caller's decision to parse an effective error status.
func unwrapQoderJSONResponse(body []byte) ([]byte, int, error) {
	trimmed := []byte(strings.TrimSpace(string(body)))
	if len(trimmed) == 0 {
		return nil, http.StatusOK, nil
	}
	var wrapper struct {
		Body            json.RawMessage `json:"body"`            // error or envelope body
		StatusCodeValue int             `json:"statusCodeValue"` // numeric HTTP status
		StatusCode      string          `json:"statusCode"`      // effective HTTP status
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
				// A string wrapper body can hold COSY text instead of JSON. Try decoding
				// that representation, retaining the original inner text when decoding
				// fails.
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

// Error formats vendor error, HTTP status, or agent-reset information for the
// caller. Nil receiver returns empty text. Agent limits take precedence and
// format a known reset in Asia/Shanghai before ordinary code/message formatting.
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

// IsAgentLimit reports whether the error contains code 115 or a positive agent
// reset timestamp. Nil errors return false; either observation can select
// agent-limit formatting and cooldown handling.
func (e *APIError) IsAgentLimit() bool {
	return e != nil && (e.Code == "115" || e.AgentLimitResetTime > 0)
}

// IsEntitlementDenied reports the vendor's code 112 model or account
// entitlement rejection. Nil errors return false, and MayRefreshAttempt
// excludes this classification before considering authentication-status
// refresh eligibility.
func (e *APIError) IsEntitlementDenied() bool {
	return e != nil && e.Code == "112"
}

// AgentLimitResetAt returns the positive agent reset timestamp interpreted as
// Unix milliseconds. Nil errors and absent or nonpositive timestamps report no
// reset time, allowing callers to choose their usual cooldown policy.
func (e *APIError) AgentLimitResetAt() (time.Time, bool) {
	if e == nil || e.AgentLimitResetTime <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(e.AgentLimitResetTime), true
}

// ParseAPIErrorBody extracts HTTP/vendor details and redacts the error body and
// message. Invalid or empty JSON keeps the generic HTTP message; nested code and
// reset metadata are resolved before body/message redaction.
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

// RedactSensitiveText trims error text and hides recognized Qoder secrets while
// retaining numeric vendor codes. Empty input returns empty text. Inline bearer,
// cookie, and token forms are processed before and after the shared text
// redactor so structured and unstructured messages use the same protection.
//
// The shared redactor can hide a code field. Recognized numeric code forms of
// one through eight digits are restored from the original text, keeping retry
// and diagnostic information available through the recognized code fields.
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

// redactQoderInlineSecrets replaces recognized inline bearer, cookie, and
// credential values with markers.
func redactQoderInlineSecrets(input string) string {
	redacted := qoderBearerTokenPattern.ReplaceAllString(input, `${1}***`)
	redacted = qoderCookiePattern.ReplaceAllString(redacted, `${1}${2}***`)
	redacted = qoderInlineSecretPattern.ReplaceAllString(redacted, `${1}${2}***`)
	return redacted
}

// restoreQoderNumericCode reinstalls recognized decimal code fields after
// generic redaction. JSON string, JSON number, and plain code forms are tried
// in order; each accepts one through eight digits and uses its first original
// match.
//
// Number and plain patterns use a word boundary after their digits, so they can
// match an integer prefix before a decimal point. Every redacted marker matched
// by the chosen form receives that same value.
// The rule treats those numeric code fields as vendor diagnostics, while
// unrecognized originals leave the redactor's output unchanged. Callers
// extract structured error classification separately before applying this text
// restoration.
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

// redactQoderAPIError mutates an error's body and message through the Qoder text
// redactor. Nil input is ignored, and vendor code/status fields remain available
// separately for retry and cooldown classification.
func redactQoderAPIError(apiErr *APIError) {
	if apiErr == nil {
		return
	}
	apiErr.Body = RedactSensitiveText(apiErr.Body)
	apiErr.Message = RedactSensitiveText(apiErr.Message)
}

// applyQoderErrorPayload reads nested vendor code, message, and positive reset
// metadata into an error. Nil target, empty payload, or malformed JSON leaves
// the existing values available.
//
// Nonempty fields replace current observations, then nested data and JSON-valued
// message text are inspected in order. A nested error can therefore supply the
// effective vendor code or reset timestamp while the outer HTTP status stays on
// APIError. Redaction runs after extraction in the public parsers.
func applyQoderErrorPayload(apiErr *APIError, payload []byte) {
	if apiErr == nil || len(payload) == 0 {
		return
	}
	var body qoderErrorBody
	if err := json.Unmarshal(payload, &body); err != nil {
		// Unrecognized vendor text still has an outer HTTP failure classification.
		// Keep that classification and leave the raw diagnostic text for redaction.
		return
	}
	if code := strings.TrimSpace(string(body.Code)); code != "" {
		apiErr.Code = code
	}
	if strings.TrimSpace(body.Message) != "" {
		apiErr.Message = body.Message
	} else if body.Type != "" && (apiErr.Message == "" || strings.HasPrefix(apiErr.Message, "Qoder upstream returned HTTP ")) {
		apiErr.Message = body.Type
	}
	if body.AgentLimitResetTime > 0 {
		apiErr.AgentLimitResetTime = body.AgentLimitResetTime
	}
	// Nested data and JSON-valued message text can carry the effective vendor
	// rejection. Inspect them after outer fields so their code and reset
	// observations can replace the enclosing values.
	if len(body.Data) > 0 {
		applyQoderErrorPayload(apiErr, body.Data)
	}
	if len(body.Error) > 0 {
		applyQoderErrorPayload(apiErr, body.Error)
	}
	if strings.TrimSpace(body.Message) != "" && json.Valid([]byte(body.Message)) {
		applyQoderErrorPayload(apiErr, []byte(body.Message))
	}
}

// setHeaders constructs Gateway authentication from a complete stable session
// and the encoded body. Callers provide the exact transmitted body and request
// path; pathWithoutAlgo strips transport prefix and query before signing. Client
// version selection also feeds the encoded identity header.
//
// COSY Authorization binds payload, key, Unix date, body, and logical path.
// Basic site headers and Appcode date/signature accompany it, while CN and
// global choose distinct data-policy and organization/business headers. Session
// identity and machine references are read synchronously and must remain stable
// during request preparation.
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
		// Global inference follows the CLI agent header shape. Add its scene and
		// business fields here while CN keeps the desktop header shape selected by
		// site.
		req.Header.Set("cosy-scene", "assistant")
		req.Header.Set("cosy-business-product", "cli")
		req.Header.Set("cosy-business-type", "agent")
	}
	req.Header.Set("Authorization", ComposeBearer(payloadB64, signature))
}

// setSignatureHeaders installs machine and Appcode headers for a pre-login
// Gateway request. A usable machine is required, while identity and encrypted
// COSY parameters can be absent because completion obtains that identity.
func (c *Client) setSignatureHeaders(req *http.Request, session *SessionContext) {
	c.setBasicHeaders(req, session)
	httpDate := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", httpDate)
	req.Header.Set("Signature", SignCenterRequest(httpDate))
	req.Header.Set("Appcode", AppCode)
}

// setBearerHeaders installs basic machine headers and trimmed security OAuth
// Bearer authentication. The request implementation validates a nonempty token
// before this call; callers supply a complete session identity and machine.
func (c *Client) setBearerHeaders(req *http.Request, session *SessionContext) {
	c.setBasicHeaders(req, session)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(session.Identity.SecurityOauthToken))
}

// setBasicHeaders writes machine, version, and client headers and returns
// their effective site. A nonempty session site overrides the client site, and
// callers provide stable machine metadata. CN uses the machine ID with empty
// token/type fields and client type zero; other sites fill omitted legacy
// token/type values from machine ID and five.
//
// CN client IP prefers ClientIP before local interface discovery, while global
// client IP uses the machine ID. MachineOS can use its configured override or
// current runtime value. Version selection follows requestClientVersion so
// these basic headers agree with the encrypted signing payload's selected
// version.
func (c *Client) setBasicHeaders(req *http.Request, session *SessionContext) Site {
	mid := session.Machine.MachineID
	machineToken := strings.TrimSpace(session.Machine.MachineToken)
	machineType := strings.TrimSpace(session.Machine.MachineType)
	site := c.Site
	if session.Site != "" {
		site = session.Site
	}
	if site == SiteCN {
		// CN uses the stored machine ID with empty machine token/type fields. Clear
		// legacy random values so imported credentials still produce its desktop
		// header shape.
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
		// The CN profile follows the desktop IDE header shape. Use its client-type
		// value with empty machine fields so the Gateway sees one consistent site
		// representation.
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

// requestClientVersion prefers the client version, then the session version,
// then global defaults. Values are trimmed before selection; nil session is
// accepted when only client or default version is available.
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

// UnmarshalJSON accepts a vendor code as trimmed string or formatted JSON
// number. Malformed JSON returns an error; other decoded types leave the
// receiver unchanged for the enclosing error parser.
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

// UnmarshalJSON decodes tolerated numeric token values and optional usage detail
// objects. Malformed usage input is ignored, leaving prior receiver values
// available. Numeric strings and floats are converted through qoderIntField;
// optional detail parsing uses zero values on failure.
func (u *QoderSSEUsage) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	// Invalid usage objects leave existing receiver values available while
	// content parsing continues. The outer usage pointer still determines whether
	// ParseSSELine emits a present, potentially zero-count observation.
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	u.PromptTokens = qoderIntField(raw, "prompt_tokens")
	u.CompletionTokens = qoderIntField(raw, "completion_tokens")
	u.TotalTokens = qoderIntField(raw, "total_tokens")
	u.InputTokens = qoderIntField(raw, "input_tokens")
	u.OutputTokens = qoderIntField(raw, "output_tokens")
	// Prompt and completion details come from successfully decoded generic JSON,
	// so their values can be marshaled again before count decoding. Keep allocated
	// detail pointers even when tolerated fields yield zero; presence remains
	// available to cache splitting and client breakdown output.
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

// UnmarshalJSON decodes cached and cacheable counts using the tolerated numeric
// field reader. Malformed input is ignored, leaving receiver values available;
// missing or unsupported numeric fields become zero.
func (d *PromptTokensDetails) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	// Prompt details are optional usage metadata. Keep the receiver on
	// object-decode failure; supported numeric fields use the same zero-result
	// reader as primary counts.
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	d.CachedTokens = qoderIntField(raw, "cached_tokens")
	d.CacheableTokens = qoderIntField(raw, "cacheable_tokens")
	return nil
}

// UnmarshalJSON decodes reasoning token counts using the tolerated numeric field
// reader. Malformed input is ignored, leaving receiver values available; a
// missing or unsupported reasoning_tokens field becomes zero.
func (d *CompletionTokensDetails) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	// Reasoning detail is optional usage metadata. Keep the receiver on
	// object-decode failure so an unusable breakdown cannot reject the whole
	// content stream.
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	d.ReasoningTokens = qoderIntField(raw, "reasoning_tokens")
	return nil
}

// qoderIntField reads floating JSON values or trimmed numeric strings as
// optional usage counts. Finite, representable floating values truncate toward
// zero. Missing fields, unsupported types, and strings rejected by both
// parsers become zero; nonfinite or unrepresentable floating values reach
// direct Go integer conversion without range validation.
func qoderIntField(raw map[string]any, key string) int {
	switch value := raw[key].(type) {
	case float64:
		// Finite, representable floating counts truncate toward zero during conversion.
		return int(value)
	case string:
		trimmed := strings.TrimSpace(value)
		if parsed, err := strconv.Atoi(trimmed); err == nil {
			return parsed
		}
		// Decimal or exponent strings can describe token counts rejected by Atoi.
		// Try floating parsing before the zero result, retaining the direct
		// conversion behavior for accepted nonfinite or out-of-range values.
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int(parsed)
		}
	}
	// Unavailable numeric fields contribute zero to optional usage decoding. That
	// result also covers unsupported representations after both accepted
	// numeric-string forms were tried.
	return 0
}

// ParseSSELine converts one wrapped SSE data line into ordered content, tool,
// usage, and completion events. Non-data lines and empty bodies return nil;
// outer or inner malformed JSON returns an error, and wrapped failure statuses
// return APIError before content parsing.
//
// Choice deltas and final messages are both inspected, alongside supported event
// envelopes. Usage aliases fill zero primary counts and derive total when
// absent. A final-message payload with finish_reason adds completion after
// content and usage, while standalone or wrapped DONE produces an immediate
// completion event.
func ParseSSELine(line string) ([]SSEEvent, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return nil, nil
	}
	data := strings.TrimSpace(line[5:])
	// Empty data is upstream heartbeat activity, not a malformed Qoder wrapper.
	// Consume it without emitting an event or interrupting later payload decoding.
	if data == "" {
		return nil, nil
	}

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
			// The two wire protocols use different input/output names. Fill zero primary
			// counts from their aliases before deriving the event total for downstream
			// usage conversion.
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

// qoderWrapperHTTPStatus resolves a wrapper's numeric status before its text
// status form. Text can be a decimal HTTP code or a recognized error name; blank
// and unknown forms return zero.
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

// appendQoderToolCallEvents normalizes wire tool calls into identity-bearing
// stream deltas. Nested function arguments win over flat arguments, input, and
// parameters; named and ID fields also follow their declared precedence.
// Explicit indices remain available, while multi-call arrays without indices
// receive compact synthetic positions after placeholder removal. A type-only
// function placeholder contributes no event or slot. This lets the accumulator
// associate later parameters with actual parallel calls rather than empty parser
// entries.
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
			// Placeholder removal changes array positions. Give each retained parsed
			// entry a compact position so downstream writers receive those references;
			// the accumulator separately decides how incomplete identity can be used.
			event.ToolCallIndex = syntheticIndex
			event.HasToolCallIndex = true
		}
		events = append(events, event)
		syntheticIndex++
	}
	return events
}

// qoderSSEToolCallIsPlaceholder reports calls lacking ID, name, and nonblank
// arguments with a function-like type. Index alone does not make the entry
// meaningful; a different supplied tool type keeps it available.
func qoderSSEToolCallIsPlaceholder(tc QoderSSEToolCall, arguments string) bool {
	meaningful := firstNonEmptyQoderSSEString(tc.ID, tc.ToolCallID, tc.CallID, tc.Function.Name, tc.Name, tc.ToolName)
	if meaningful != "" || strings.TrimSpace(arguments) != "" {
		return false
	}
	return strings.TrimSpace(tc.Type) == "" || qoderSSEToolType(tc.Type) == "function"
}

// qoderSSEEnvelopeEvents dispatches recognized event/type envelopes into
// normalized stream events. Event takes precedence over Type, message_stop marks
// completion, and unknown or missing event labels return no events.
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

// qoderSSEContentBlockDeltaEvents extracts nonempty text from a content-block
// delta envelope. Malformed or absent data produces no event; thinking_delta
// becomes reasoning_delta and other text-bearing blocks become text_delta.
func qoderSSEContentBlockDeltaEvents(raw json.RawMessage) []SSEEvent {
	if len(raw) == 0 {
		return nil
	}
	var delta struct {
		Type string `json:"type"` // wire block type
		Text string `json:"text"` // text or reasoning delta
	}
	// Malformed or empty optional block data contributes no readable fragment.
	// Omit that envelope so later valid content can still be normalized.
	if err := json.Unmarshal(raw, &delta); err != nil || delta.Text == "" {
		return nil
	}
	eventType := "text_delta"
	if delta.Type == "thinking_delta" {
		eventType = "reasoning_delta"
	}
	return []SSEEvent{{Type: eventType, Text: delta.Text}}
}

// qoderSSEToolUseStartEvents creates a tool-start delta from supported identity
// fields. Absent or malformed data produces no event. The data index takes
// precedence over envelope index, retaining explicit zero as an available index,
// and ID aliases are checked in order.
//
// A start can have empty arguments; its ID, index, or name supplies later
// association information. Missing tool type becomes function for protocol
// output, while that default alone does not identify the call receiving later
// parameters.
func qoderSSEToolUseStartEvents(raw json.RawMessage, envelopeIndex *int) []SSEEvent {
	if len(raw) == 0 {
		return nil
	}
	var toolUse struct {
		// Index retains pointer presence so an explicit zero position can differ from
		// a missing position. Event normalization carries this into HasToolCallIndex.
		Index      *int   `json:"index,omitempty"` // optional call position
		ID         string `json:"id"`              // preferred call identifier
		ToolCallID string `json:"tool_call_id"`    // upstream call identifier
		CallID     string `json:"call_id"`         // alternate call identifier
		Name       string `json:"name"`            // function name text
		Type       string `json:"type"`            // wire tool type
	}
	// A malformed start cannot establish a usable invocation identity. Drop this
	// optional envelope while allowing other supported stream observations to
	// continue.
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

// qoderSSEToolUseDeltaEvents extracts identity and arguments from a tool
// continuation envelope. Absent or malformed data produces no event. Argument
// aliases are decoded in order, and the data index takes precedence over
// envelope index, including zero.
//
// A function-like type with no ID, name, or arguments is dropped as a
// placeholder. Argument-only events remain available to the accumulator, which
// decides whether a single existing call identifies their destination or
// parallel calls make them ambiguous.
func qoderSSEToolUseDeltaEvents(raw json.RawMessage, envelopeIndex *int) []SSEEvent {
	if len(raw) == 0 {
		return nil
	}
	var delta struct {
		// Index retains pointer presence so an explicit zero position can differ from
		// a missing position. Event normalization carries this into HasToolCallIndex.
		Index      *int            `json:"index,omitempty"` // optional call position
		ID         string          `json:"id"`              // preferred call identifier
		ToolCallID string          `json:"tool_call_id"`    // upstream call identifier
		CallID     string          `json:"call_id"`         // alternate call identifier
		Name       string          `json:"name"`            // function name text
		ToolName   string          `json:"tool_name"`       // upstream function name
		Type       string          `json:"type"`            // wire tool type
		Arguments  json.RawMessage `json:"arguments"`       // JSON argument text
		Input      json.RawMessage `json:"input"`           // alternate argument JSON
		Parameters json.RawMessage `json:"parameters"`      // alternate argument JSON
	}
	// Unreadable continuation metadata cannot identify a destination or argument
	// fragment. Omit this optional envelope so it cannot corrupt an accumulated
	// call.
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
	// A function-type default alone contains no usable call identity or argument
	// fragment. Drop that placeholder before assigning any envelope position to it.
	if event.ToolCallID == "" && event.ToolName == "" && strings.TrimSpace(event.Arguments) == "" && event.ToolType == "function" {
		return nil
	}
	if index := firstNonNilQoderSSEInt(delta.Index, envelopeIndex); index != nil {
		event.ToolCallIndex = *index
		event.HasToolCallIndex = true
	}
	return []SSEEvent{event}
}

// qoderSSEMessageDeltaEvents converts envelope usage into an observed token
// event. Missing data or a nil usage pointer produces no event. The usage
// decoder tolerates malformed objects, so a present pointer can yield a zero-
// count observation.
//
// Zero prompt and completion counts use input/output aliases before a zero
// total is derived from their sum. Detail pointers are retained for downstream
// cache splitting and serialization. This sequence gives envelope usage the
// same alias rules as choice usage while retaining the distinction between
// present zero usage and absent metadata.
func qoderSSEMessageDeltaEvents(raw json.RawMessage) []SSEEvent {
	if len(raw) == 0 {
		return nil
	}
	var delta struct {
		Usage *QoderSSEUsage `json:"usage"` // optional usage payload
	}
	// An absent usage pointer contributes no observation, while an unreadable
	// enclosing delta has no usable payload to pass to downstream accounting.
	if err := json.Unmarshal(raw, &delta); err != nil || delta.Usage == nil {
		return nil
	}
	promptTokens := delta.Usage.PromptTokens
	if promptTokens == 0 {
		// Event-envelope usage can use input/output aliases instead of Chat names.
		// Apply the same count fallback as choice usage so both representations
		// produce matching observations.
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

// qoderSSEContentText extracts string content or joins text blocks with
// newlines. Unsupported representations and non-text blocks contribute empty
// text, allowing the final-message parser to handle wire content variants.
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

// qoderSSEMessageHasPayload reports content presence, reasoning text, or tool
// calls in a final message.
func qoderSSEMessageHasPayload(message QoderSSEMessage) bool {
	return message.Content != nil || message.ReasoningContent != "" || len(message.ToolCalls) > 0
}

// parseQoderToolCallArguments selects the first nonempty argument representation
// in caller-specified order. JSON strings are unquoted, null and blank strings
// are skipped, and other raw JSON retains its text for later assembly.
func parseQoderToolCallArguments(values ...json.RawMessage) string {
	for _, raw := range values {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var s string
		// Arguments can be JSON-encoded strings or complete raw objects. Unquote
		// string fragments while retaining object JSON text for the accumulator's
		// later concatenation and validation.
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

// firstNonEmptyQoderSSEString selects the first value containing non-
// whitespace text and retains its supplied spelling. Missing candidates and an
// entirely blank list return empty text for the parser's next default.
func firstNonEmptyQoderSSEString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// firstNonNilQoderSSEInt returns the first supplied pointer, preserving
// explicit zero as an available index. An empty or entirely nil list returns
// nil; the chosen pointer still refers to the caller's integer.
func firstNonNilQoderSSEInt(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// qoderSSEToolType maps empty, tool_use, and tool_call types to function for
// compatibility. Other type strings retain their supplied spelling; callers keep
// identity selection separate from this default.
func qoderSSEToolType(value string) string {
	switch strings.TrimSpace(value) {
	case "", "tool_use", "tool_call":
		return "function"
	default:
		return value
	}
}

// parseWrappedAPIError extracts and redacts vendor details from an already
// failed SSE wrapper. The wrapper's effective numeric status is retained, while
// absent details keep a generic HTTP failure message.
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

// StreamEvents starts a scanner goroutine and returns its buffered event
// channel. The goroutine owns body and channel closure. Callers drain the
// channel so sends can finish; closing the body can release a blocked read but
// cannot release a blocked send.
//
// A parser failure emits a terminal error event, and a completion event ends
// scanning. Scanner errors are omitted by this standalone helper. Executor
// paths use StreamQoderEvents for cancellation-aware sends and scanner error
// reporting, so early-stopping consumers choose their reader according to
// those lifetime requirements.
func StreamEvents(resp *http.Response) <-chan SSEEvent {
	ch := make(chan SSEEvent, 16)
	// The returned channel transfers events from a scanner that owns body cleanup.
	// Drain it so a blocked send can reach its completion and close the response.
	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		scanner := bufio.NewScanner(resp.Body)
		// Bound each line while permitting large wrapped tool arguments.
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
