package qoder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GenerationPath is the SSE streaming endpoint for Qoder LLM inference.
const GenerationPath = "/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"

// Client is an HTTP client for the Qoder API with COSY protocol support.
type Client struct {
	APIBaseURL    string
	ClientVersion string
	HTTPClient    *http.Client
}

// RequestDoer executes a prepared HTTP request.
type RequestDoer func(req *http.Request) (*http.Response, error)

// NewClient creates a new Qoder API client.
func NewClient(apiBaseURL string) *Client {
	if apiBaseURL == "" {
		apiBaseURL = APIBaseURL
	}
	return &Client{
		APIBaseURL:    strings.TrimRight(apiBaseURL, "/"),
		ClientVersion: ClientVersion,
		HTTPClient:    &http.Client{},
	}
}

// StreamRequest sends a streaming POST request to the Qoder API and returns an SSE line reader.
func (c *Client) StreamRequest(session *SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string) (*http.Response, error) {
	return c.StreamRequestContext(context.Background(), session, path, bodyJSON, extraHeaders)
}

// StreamRequestContext sends a streaming POST request to the Qoder API and returns an SSE line reader.
func (c *Client) StreamRequestContext(ctx context.Context, session *SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string) (*http.Response, error) {
	return c.StreamRequestContextWithDoer(ctx, session, path, bodyJSON, extraHeaders, nil)
}

// StreamRequestContextWithDoer sends a streaming POST request using the provided executor.
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
		resp.Body.Close()
		apiErr := ParseAPIErrorBody(resp.StatusCode, string(body))
		if apiErr == nil {
			apiErr = &APIError{}
		}
		apiErr.StatusCode = resp.StatusCode
		apiErr.Body = string(body)
		if strings.TrimSpace(apiErr.Message) == "" {
			apiErr.Message = fmt.Sprintf("Qoder upstream returned HTTP %d", resp.StatusCode)
		}
		return nil, apiErr
	}

	return resp, nil
}

// APIError represents an error from the Qoder API.
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

// IsAgentLimit reports whether this error is Qoder's agent quota/rate limit.
func (e *APIError) IsAgentLimit() bool {
	return e != nil && (e.Code == "115" || e.AgentLimitResetTime > 0)
}

// AgentLimitResetAt returns the parsed Qoder agent limit reset time.
func (e *APIError) AgentLimitResetAt() (time.Time, bool) {
	if e == nil || e.AgentLimitResetTime <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(e.AgentLimitResetTime), true
}

// ParseAPIErrorBody parses Qoder HTTP/SSE error bodies.
func ParseAPIErrorBody(statusCode int, body string) *APIError {
	apiErr := &APIError{
		StatusCode: statusCode,
		Body:       body,
		Message:    fmt.Sprintf("Qoder upstream returned HTTP %d", statusCode),
	}
	applyQoderErrorPayload(apiErr, []byte(body))
	return apiErr
}

func applyQoderErrorPayload(apiErr *APIError, payload []byte) {
	if apiErr == nil || len(payload) == 0 {
		return
	}
	var body qoderErrorBody
	if err := json.Unmarshal(payload, &body); err != nil {
		return
	}
	if strings.TrimSpace(body.Code) != "" {
		apiErr.Code = body.Code
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
	payloadB64, _ := BuildPayloadB64(session.Info, GenerateRequestID())
	signature := SignQoderRequest(payloadB64, session.CosyKey, now, encodedBody, pathNoAlgo)

	mid := session.Machine.MachineID

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Go-http-client/2.0")
	req.Header.Set("Login-Version", "v2")
	req.Header.Set("cosy-data-policy", "disagree")
	req.Header.Set("cosy-version", c.ClientVersion)
	req.Header.Set("cosy-clienttype", "5")
	req.Header.Set("cosy-clientip", mid)
	req.Header.Set("cosy-date", now)
	req.Header.Set("cosy-key", session.CosyKey)
	req.Header.Set("cosy-user", session.Identity.UID)
	req.Header.Set("cosy-machineid", mid)
	req.Header.Set("cosy-machinetype", "5")
	req.Header.Set("cosy-machinetoken", mid)
	req.Header.Set("cosy-scene", "assistant")
	req.Header.Set("cosy-organization-id", session.Identity.OrganizationID)
	req.Header.Set("cosy-organization-tags", "Normal")
	req.Header.Set("cosy-business-product", "cli")
	req.Header.Set("cosy-business-type", "agent")
	req.Header.Set("Authorization", ComposeBearer(payloadB64, signature))
}

// SSEEvent represents a parsed SSE event from the Qoder stream.
type SSEEvent struct {
	Type             string // text_delta, reasoning_delta, tool_call_delta, usage, error
	Text             string // For text_delta and reasoning_delta events
	ToolCallID       string // For tool_call_delta events
	ToolName         string // For tool_call_delta events
	Arguments        string // For tool_call_delta events (JSON string)
	PromptTokens     int    // For usage events
	CompletionTokens int    // For usage events
	TotalTokens      int    // For usage events
	HasUsage         bool   // True when Qoder returned a usage payload
	IsDone           bool   // True when [DONE] signal received
}

// QoderSSEWrapper is the outer SSE structure from Qoder.
type QoderSSEWrapper struct {
	Body            string `json:"body"`
	StatusCode      string `json:"statusCode"`
	StatusCodeValue int    `json:"statusCodeValue"`
}

type qoderErrorBody struct {
	Code                string          `json:"code"`
	Message             string          `json:"message"`
	Data                json.RawMessage `json:"data"`
	AgentLimitResetTime int64           `json:"agentLimitResetTime"`
}

// QoderSSEInner is the inner structure of a Qoder SSE body.
type QoderSSEInner struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *QoderSSEUsage `json:"usage,omitempty"`
}

// QoderSSEUsage is the token usage object Qoder includes in SSE payloads.
type QoderSSEUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
}

// ParseSSELine parses a single SSE "data:" line from Qoder's stream.
// Returns nil if the line doesn't contain any meaningful events.
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

	if wrapper.StatusCodeValue >= http.StatusBadRequest {
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

	var events []SSEEvent
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

		for _, tc := range delta.ToolCalls {
			events = append(events, SSEEvent{
				Type:       "tool_call_delta",
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
				Arguments:  tc.Function.Arguments,
			})
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
			HasUsage:         true,
		})
	}

	return events, nil
}

func parseWrappedAPIError(wrapper QoderSSEWrapper) error {
	apiErr := &APIError{
		StatusCode: wrapper.StatusCodeValue,
		Body:       wrapper.Body,
		Message:    fmt.Sprintf("Qoder upstream returned HTTP %d", wrapper.StatusCodeValue),
	}
	applyQoderErrorPayload(apiErr, []byte(wrapper.Body))
	return apiErr
}

// StreamEvents reads SSE lines from a response body and returns a channel of events.
func StreamEvents(resp *http.Response) <-chan SSEEvent {
	ch := make(chan SSEEvent, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

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

// ParseSSEEvent parses a single SSE data line and returns the first event.
// This is a convenience wrapper around ParseSSELine for single-event consumption.
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

// Ensure url package usage
var _ = url.Parse
