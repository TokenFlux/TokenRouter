package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// output.go captures finite management responses and converts their business
// result into MCP content. A bounded writer cancels the request when output
// exceeds its allowance; completed HTTP headers alone cannot establish whether
// a streaming operation finished. SSE success therefore requires the declared
// terminal event and an absence of error events.
//
// Credential exports and large results are stored as encrypted, owned files
// instead of being expanded into model content. Storage failure after a handler
// has returned is reported as an uncertain mutation outcome, because delivery
// failure cannot roll back that handler's side effects. Inline JSON and finite
// stream events use the credential's response masking preference. The original
// HTTP handler still determines which fields are available in its response.

const (
	// Larger JSON results become protected files so one broad query cannot fill
	// the agent context with an unbounded provider, user, or usage listing.
	maxInlineResultBytes = 128 << 10
)

// capture records one finite internal response and cancels oversized output.
type capture struct {
	header http.Header        // response metadata accumulator
	status int                // first committed HTTP status
	body   bytes.Buffer       // bounded response body
	err    error              // first capture failure
	cancel context.CancelFunc // request cancellation owner
	closed chan bool          // legacy Gin stream cancellation
	stop   func() bool        // cancellation callback removal
}

// newCapture creates bounded output storage and a request-cancellation signal.
// The caller closes the capture after dispatch, which removes the callback;
// stream consumers receive cancellation without retaining a goroutine per request.
func newCapture(ctx context.Context, cancel context.CancelFunc) *capture {
	c := &capture{header: make(http.Header), cancel: cancel, closed: make(chan bool, 1)}
	c.stop = context.AfterFunc(ctx, func() { c.closed <- true })
	return c
}

// Header returns the response header map used by the existing handler.
func (c *capture) Header() http.Header { return c.header }

// WriteHeader retains the first HTTP response status committed by the handler.
func (c *capture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

// Write records bytes while capacity remains and cancels on overflow.
// Overflow retains the preceding bytes only for diagnostics inside this call;
// the executor reports interruption and never returns a truncated success result.
func (c *capture) Write(data []byte) (int, error) {
	c.WriteHeader(http.StatusOK)
	if c.err != nil {
		return 0, c.err
	}
	if len(data) > maxResultBytes-c.body.Len() {
		c.err = errors.New("operation result exceeds capture limit")
		// Cancellation asks the existing upstream or database operation to stop;
		// it cannot undo writes already committed before the oversized response arrived.
		c.cancel()
		return 0, c.err
	}
	return c.body.Write(data)
}

// Flush commits response metadata while retaining stream bytes for terminal verification.
func (c *capture) Flush() { c.WriteHeader(http.StatusOK) }

// CloseNotify provides the cancellation signal expected by legacy Gin stream handlers.
func (c *capture) CloseNotify() <-chan bool { return c.closed }

// close removes the cancellation callback after the internal response finishes.
func (c *capture) close() { c.stop() }

// operationResult converts a completed HTTP response into a structured MCP
// result. HTTP server errors on mutations remain uncertain because a handler
// may fail after committing its business write. Ordinary client errors
// preserve their status and apply the same masking preference as successes.
//
// Finite streams require their declared terminal event. Files and oversized
// results are stored with token ownership before a browser link is returned;
// storage failure cannot undo the completed handler. The caller remains
// responsible for detecting capture cancellation before this conversion.
func (h *Handler) operationResult(ctx context.Context, token *identity.MCPToken, op *adminapi.Operation, response *capture) *mcp.CallToolResult {
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	requestID := response.header.Get("X-Request-ID")
	var decoded any
	if response.body.Len() > 0 && json.Valid(response.body.Bytes()) {
		// Keep identifiers and decimal amounts in their original JSON number form;
		// conversion to float64 would change the result before it reaches the model.
		decoder := json.NewDecoder(bytes.NewReader(response.body.Bytes()))
		decoder.UseNumber()
		_ = decoder.Decode(&decoded)
	}
	if status >= 300 {
		outcome := "failed"
		if status >= 500 && !op.ReadOnly {
			outcome = "outcome_unknown"
		}
		result := toolResult(map[string]any{
			"status": outcome, "http_status": status, "request_id": requestID,
			"data": h.toolResultData(token, op.ID, decoded, "response"),
		})
		result.IsError = true
		return result
	}
	if op.Response == adminapi.ResponseSSE {
		result := h.streamResult(token, op, response.body.Bytes(), requestID)
		if response.body.Len() <= maxInlineResultBytes {
			return result
		}
		file := h.newArtifact(token, op.ID)
		file.Name = op.ID + ".json"
		file.ContentType = "application/json"
		var err error
		file.Data, err = json.Marshal(result.StructuredContent)
		if err != nil || h.state.SaveArtifact(ctx, file) != nil {
			return uncertainResult(op.ReadOnly, "The stream finished but its protected result could not be stored.")
		}
		metadata, metadataOK := h.artifactResult(ctx, file).StructuredContent.(map[string]any)
		outcome, outcomeOK := result.StructuredContent.(map[string]any)
		if !metadataOK || !outcomeOK {
			return uncertainResult(op.ReadOnly, "The stream result could not be represented.")
		}
		metadata["operation_status"] = outcome["status"]
		protected := toolResult(metadata)
		protected.IsError = result.IsError
		return protected
	}
	if op.Response == adminapi.ResponseFile || response.body.Len() > maxInlineResultBytes || decoded == nil && response.body.Len() > 0 {
		file := h.newArtifact(token, op.ID)
		file.Name = artifactFilename(op.ID, response.header)
		file.ContentType = response.header.Get("Content-Type")
		file.Data = bytes.Clone(response.body.Bytes())
		if err := h.state.SaveArtifact(ctx, file); err != nil {
			return uncertainResult(op.ReadOnly, "The operation finished but its protected result could not be stored.")
		}
		return h.artifactResult(ctx, file)
	}
	return toolResult(map[string]any{
		"status": "succeeded", "http_status": status, "request_id": requestID,
		"data": h.toolResultData(token, op.ID, decoded, "response"),
	})
}

// streamResult joins SSE data lines and verifies the finite operation outcome.
// Every event must contain valid JSON, and success requires the catalogue's
// terminal event with a successful result. An error event remains
// authoritative even when a later event claims completion.
//
// A missing terminal or malformed stream cannot establish mutation failure, so
// it returns an unknown outcome for writes. The credential's redaction
// preference controls masking of event data after the outcome checks read it.
// Numeric event values retain their decimal spelling so result rendering does
// not round identifiers or precise observation amounts.
func (h *Handler) streamResult(token *identity.MCPToken, op *adminapi.Operation, raw []byte, requestID string) *mcp.CallToolResult {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), maxResultBytes)
	var lines []string
	events := []any{}
	complete, failed, malformed := false, false, false
	consume := func() {
		if len(lines) == 0 {
			return
		}
		data := strings.Join(lines, "\n")
		lines = nil
		var event map[string]any
		if !json.Valid([]byte(data)) {
			malformed = true
			return
		}
		decoder := json.NewDecoder(strings.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&event); err != nil {
			malformed = true
			return
		}
		kind, _ := event["type"].(string)
		if kind == op.TerminalEvent {
			complete = true
			success, _ := event["success"].(bool)
			failed = failed || !success
		}
		if kind == "error" {
			failed = true
		}
		root := "event"
		if kind == "error" {
			// Only error events establish that a top-level code describes failure;
			// progress and successful terminal payloads may instead carry a protected credential code.
			root = "event/error"
		}
		events = append(events, h.toolResultData(token, op.ID, event, root))
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			consume()
		} else if data, ok := strings.CutPrefix(line, "data:"); ok {
			lines = append(lines, strings.TrimPrefix(data, " "))
		}
	}
	consume()
	if scanner.Err() != nil || malformed || !complete && !failed {
		return uncertainResult(op.ReadOnly, "The stream ended without a verified terminal result.")
	}
	state := "succeeded"
	if failed {
		state = "failed"
	}
	result := toolResult(map[string]any{"status": state, "request_id": requestID, "events": events})
	result.IsError = failed
	return result
}

// artifactFilename retains a handler's suggested filename for protected delivery.
// Responses without a filename use an operation-derived name; JSON media types
// receive a JSON extension so exported structured results remain recognizable.
func artifactFilename(operation string, header http.Header) string {
	_, parameters, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err == nil && parameters["filename"] != "" {
		return filepath.Base(parameters["filename"])
	}
	// Ordinary JSON handlers omit Content-Disposition. Derive the extension
	// from their response media type rather than presenting those exports as opaque binary files.
	mediaType, _, _ := mime.ParseMediaType(header.Get("Content-Type"))
	if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
		return operation + ".json"
	}
	return operation + ".bin"
}
