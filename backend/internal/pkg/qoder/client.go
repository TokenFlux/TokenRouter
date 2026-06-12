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

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: request failed: %w", err)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
			Message:    fmt.Sprintf("Qoder upstream returned HTTP %d", resp.StatusCode),
		}
	}

	return resp, nil
}

func (c *Client) setHeaders(req *http.Request, session *SessionContext, path, encodedBody string) {
	now := strconv.FormatInt(time.Now().Unix(), 10)
	pathNoAlgo := pathWithoutAlgo(path)
	payloadB64, _ := BuildPayloadB64(session.Info, GenerateRequestID())
	signature := SignQoderRequest(payloadB64, session.CosyKey, now, encodedBody, pathNoAlgo)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Go-http-client/2.0")
	req.Header.Set("Login-Version", "v2")
	req.Header.Set("cosy-data-policy", "AGREE")
	req.Header.Set("cosy-version", c.ClientVersion)
	req.Header.Set("cosy-clienttype", "5")
	req.Header.Set("cosy-clientip", "169.254.198.161")
	req.Header.Set("cosy-date", now)
	req.Header.Set("cosy-key", session.CosyKey)
	req.Header.Set("cosy-user", session.Identity.UID)
	req.Header.Set("cosy-machineid", session.Machine.MachineID)
	req.Header.Set("cosy-machinetype", session.Machine.MachineType)
	req.Header.Set("cosy-machinetoken", session.Machine.MachineToken)
	req.Header.Set("Authorization", ComposeBearer(payloadB64, signature))
}

// APIError represents an error from the Qoder API.
type APIError struct {
	StatusCode int
	Body       string
	Message    string
}

func (e *APIError) Error() string {
	return e.Message
}

// SSEEvent represents a parsed SSE event from the Qoder stream.
type SSEEvent struct {
	Type       string // text_delta, tool_call_delta, error
	Text       string // For text_delta events
	ToolCallID string // For tool_call_delta events
	ToolName   string // For tool_call_delta events
	Arguments  string // For tool_call_delta events (JSON string)
	IsDone     bool   // True when [DONE] signal received
}

// QoderSSEWrapper is the outer SSE structure from Qoder.
type QoderSSEWrapper struct {
	Body string `json:"body"`
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

		if delta.ReasoningContent != "" && delta.Content == "" {
			events = append(events, SSEEvent{
				Type: "text_delta",
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

	return events, nil
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
