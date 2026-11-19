package ops

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

// error_response_parse.go parses HTTP and SSE error payloads, sanitizes
// captured SSE frames, infers logical stream failure statuses, and evaluates
// monitoring skip rules.
// A streaming request can commit HTTP 200 before an upstream failure arrives
// inside an SSE frame, and captured diagnostic prefixes can truncate multi-line
// JSON payloads. Terminal SSE frame recognition therefore precedes generic JSON
// decoding and preserves failure classification even when JSON bytes are cut.
//
// Sanitization joins multi-line SSE data before credential redaction and
// replaces truncated JSON fragments with an explicit marker so partial keys
// cannot reach storage. Monitoring suppression and advanced filter helpers run
// on extracted text and ordered attempt lists without HTTP framework coupling.

// Lowercased diagnostic substrings select advanced error-log filter categories
// independently of HTTP wire status.
const (
	opsErrContextCanceled             = "context canceled"             // canceled request context fragment
	opsErrInvalidAPIKey               = "invalid_api_key"              // invalid credential body fragment
	opsErrAPIKeyRequired              = "api_key_required"             // missing credential body fragment
	opsErrInsufficientProviderBalance = "insufficient account balance" // provider balance exhaustion fragment
	opsErrInsufficientQuota           = "insufficient_quota"           // quota exhaustion body fragment
)

// ParsedOpsError carries extracted client failure fields and terminal stream
// evidence from an HTTP error body or SSE terminal frame.
type ParsedOpsError struct {
	ErrorType  string // extracted client error category
	Message    string // extracted failure message text
	Code       string // extracted client error code
	StatusCode int    // optional logical failure HTTP status
	// StreamFailure records recognized terminal SSE evidence even when payload
	// decoding is incomplete, allowing callers to infer a logical failure status
	// after HTTP 200 commitment.
	StreamFailure bool
}

// ParseOpsErrorResponse extracts terminal SSE failure metadata before falling
// back to generic JSON error shapes. Empty bodies return a zero value.
//
// Terminal SSE frames can arrive after HTTP 200 commitment, so structured SSE
// evidence takes precedence over treating the whole stream as an opaque string.
// Undecodable or unrecognized bodies retain a UTF-8-bounded 1024-byte prefix
// for downstream attribution.
func ParseOpsErrorResponse(body []byte) ParsedOpsError {
	if len(body) == 0 {
		return ParsedOpsError{}
	}
	if parsed, ok := ParseOpsSSEFailure(body); ok {
		return parsed
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ParsedOpsError{Message: logredact.TruncateUTF8(string(body), 1024)}
	}

	if errObj, ok := m["error"].(map[string]any); ok {
		t, _ := errObj["type"].(string)
		msg, _ := errObj["message"].(string)
		if t == "" {
			t = "api_error"
		}
		code := opsJSONScalarString(errObj["code"])
		return ParsedOpsError{ErrorType: t, Message: msg, Code: code}
	}
	if errMessage, ok := m["error"].(string); ok && strings.TrimSpace(errMessage) != "" {
		t, _ := m["type"].(string)
		if t == "" || t == "error" {
			t = "api_error"
		}
		return ParsedOpsError{
			ErrorType: t,
			Message:   strings.TrimSpace(errMessage),
			Code:      opsJSONScalarString(m["code"]),
		}
	}

	code := opsJSONScalarString(m["code"])
	msg, _ := m["message"].(string)
	if code != "" || msg != "" {
		t, _ := m["type"].(string)
		if t == "" || t == "error" {
			t = "api_error"
		}
		return ParsedOpsError{ErrorType: t, Message: msg, Code: code}
	}

	return ParsedOpsError{Message: logredact.TruncateUTF8(string(body), 1024)}
}

// ParseOpsSSEFailure extracts logical failure metadata from LF, CRLF, or CR SSE
// frames. The first response.failed frame wins immediately, while ordinary
// error frames retain the latest candidate when no Responses terminal appears.
//
// Explicit status fields resolve from nested error objects before top-level
// fields: zero advances to the next candidate, whereas a non-zero out-of-range
// status blocks fallback status fields and defers to code/type status
// inference. JSON-looking payloads without a message receive generic text so
// truncated credential JSON cannot leak into diagnostic summaries.
func ParseOpsSSEFailure(body []byte) (ParsedOpsError, bool) {
	// Normalize CR and CRLF line endings before splitting frames so mixed
	// upstream framing shares one event/data extraction rule.
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	var errorCandidate *ParsedOpsError
	for frame := range strings.SplitSeq(normalized, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		eventTypeBytes, payloadBytes := ParseOpsSSEFrameEnvelope([]byte(frame))
		eventType := string(eventTypeBytes)
		if eventType != "response.failed" && eventType != "error" && len(payloadBytes) == 0 {
			continue
		}

		payload := string(payloadBytes)
		var event map[string]any
		if err := json.Unmarshal(payloadBytes, &event); err == nil {
			if eventType == "" {
				eventType, _ = event["type"].(string)
			}
		}
		if eventType != "response.failed" && eventType != "error" {
			continue
		}

		parsed := ParsedOpsError{ErrorType: "upstream_error", StreamFailure: true}
		if eventType == "error" {
			parsed.ErrorType = "api_error"
		}
		errObj := opsSSEErrorObject(event)
		if errObj == nil && event != nil && (event["message"] != nil || event["code"] != nil) {
			errObj = event
		}
		if errObj != nil {
			parsed.ErrorType, _ = errObj["type"].(string)
			if parsed.ErrorType == "error" || parsed.ErrorType == "response.failed" {
				parsed.ErrorType = ""
			}
			parsed.Message, _ = errObj["message"].(string)
			switch code := errObj["code"].(type) {
			case string:
				parsed.Code = strings.TrimSpace(code)
			case float64:
				parsed.Code = strconv.Itoa(int(code))
			}
			parsed.StatusCode = opsJSONInt(errObj["status_code"])
			if parsed.StatusCode == 0 {
				parsed.StatusCode = opsJSONInt(errObj["status"])
			}
			if parsed.StatusCode == 0 {
				parsed.StatusCode = opsJSONInt(event["status_code"])
			}
			if parsed.StatusCode == 0 {
				parsed.StatusCode = opsJSONInt(event["status"])
			}
			if parsed.ErrorType == "" {
				parsed.ErrorType = inferResponsesFailedOpsErrorType(parsed.Code)
			}
			if parsed.ErrorType == "" {
				if eventType == "error" {
					parsed.ErrorType = "api_error"
				} else {
					parsed.ErrorType = "upstream_error"
				}
			}
		}
		// Replace blank messages on JSON-shaped payloads with generic safe text so
		// truncated JSON bearing credentials never becomes the diagnostic summary.
		if strings.TrimSpace(parsed.Message) == "" && payload != "" {
			trimmedPayload := strings.TrimSpace(payload)
			if strings.HasPrefix(trimmedPayload, "{") || strings.HasPrefix(trimmedPayload, "[") {
				parsed.Message = "upstream stream failed"
			} else {
				parsed.Message = logredact.TruncateUTF8(trimmedPayload, 1024)
			}
		}
		if eventType == "response.failed" {
			return parsed, true
		}
		candidate := parsed
		errorCandidate = &candidate
	}
	if errorCandidate != nil {
		return *errorCandidate, true
	}
	return ParsedOpsError{}, false
}

// IsOpsTerminalSSEFrame reports whether frame declares response.failed or error
// in its event header or decoded JSON payload type.
func IsOpsTerminalSSEFrame(frame []byte) bool {
	eventType, payload := ParseOpsSSEFrameEnvelope(frame)
	if bytes.Equal(eventType, []byte("response.failed")) || bytes.Equal(eventType, []byte("error")) {
		return true
	}
	if len(payload) == 0 {
		return false
	}
	// Skip JSON unmarshaling on ordinary delta frames that lack terminal type markers.
	if !bytes.Contains(payload, []byte("response.failed")) && !bytes.Contains(payload, []byte(`"error"`)) {
		return false
	}
	var event struct {
		Type string `json:"type"` // decoded SSE payload type
	}
	return json.Unmarshal(payload, &event) == nil &&
		(event.Type == "response.failed" || event.Type == "error")
}

// ParseOpsSSEFrameEnvelope extracts the last event value and newline-joined
// data payload from one SSE frame across LF, CRLF, and CR line endings.
//
// Single-line data slices borrow frame storage directly; multi-line data copies
// into a fresh slice before joining so callers' input buffers remain unmodified.
func ParseOpsSSEFrameEnvelope(frame []byte) ([]byte, []byte) {
	var eventType []byte
	var data []byte
	dataOwned := false
	dataSeen := false
	for len(frame) > 0 {
		line := frame
		lf := bytes.IndexByte(frame, '\n')
		cr := bytes.IndexByte(frame, '\r')
		idx := lf
		if idx < 0 || (cr >= 0 && cr < idx) {
			idx = cr
		}
		if idx >= 0 {
			line = frame[:idx]
			consume := idx + 1
			if frame[idx] == '\r' && consume < len(frame) && frame[consume] == '\n' {
				consume++
			}
			frame = frame[consume:]
		} else {
			frame = nil
		}
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte{':'})
		if !found {
			value = nil
		}
		field = bytes.TrimSpace(field)
		value = bytes.TrimSpace(value)
		switch {
		case bytes.Equal(field, []byte("event")):
			eventType = value
		case bytes.Equal(field, []byte("data")):
			if !dataSeen {
				data = value
				dataSeen = true
				continue
			}
			if !dataOwned {
				data = append([]byte(nil), data...)
				dataOwned = true
			}
			data = append(data, '\n')
			data = append(data, value...)
		}
	}
	return bytes.TrimSpace(eventType), data
}

// EndsAtOpsSSEFrameBoundary reports whether chunk ends with an LF, CRLF, or CR
// blank-line SSE frame separator.
func EndsAtOpsSSEFrameBoundary(chunk []byte) bool {
	return bytes.HasSuffix(chunk, []byte("\n\n")) ||
		bytes.HasSuffix(chunk, []byte("\r\n\r\n")) ||
		bytes.HasSuffix(chunk, []byte("\r\r"))
}

// MayContainOpsTerminalSSE reports whether chunk contains raw byte markers that
// could belong to a response.failed or error SSE frame.
func MayContainOpsTerminalSSE(chunk []byte) bool {
	if bytes.Contains(chunk, []byte("response.failed")) {
		return true
	}
	return bytes.Contains(chunk, []byte("error")) &&
		(bytes.Contains(chunk, []byte("event")) || bytes.Contains(chunk, []byte(`"type"`)))
}

// IsOpsTerminalSSEEventLine reports whether line is an SSE event header naming
// response.failed or error.
func IsOpsTerminalSSEEventLine(line []byte) bool {
	line = bytes.TrimSpace(line)
	field, value, found := bytes.Cut(line, []byte{':'})
	return found && bytes.Equal(bytes.TrimSpace(field), []byte("event")) &&
		(bytes.Equal(bytes.TrimSpace(value), []byte("response.failed")) ||
			bytes.Equal(bytes.TrimSpace(value), []byte("error")))
}

// SanitizeOpsSSEDataForPersistence normalizes SSE line endings and redacts
// joined JSON data payloads before queue submission. Valid JSON payloads run
// through SanitizeOpsErrorBodyForQueue, while truncated JSON fragments become
// an explicit truncation marker so partial secrets never persist.
func SanitizeOpsSSEDataForPersistence(body []byte) string {
	if len(body) == 0 || !bytes.Contains(body, []byte("data")) {
		return string(body)
	}
	normalized := bytes.ReplaceAll(body, []byte("\r\n"), []byte{'\n'})
	normalized = bytes.ReplaceAll(normalized, []byte{'\r'}, []byte{'\n'})
	frames := bytes.Split(normalized, []byte("\n\n"))
	var out bytes.Buffer
	out.Grow(len(body))
	for frameIndex, frame := range frames {
		if frameIndex > 0 {
			_, _ = out.WriteString("\n\n")
		}
		_, payload := ParseOpsSSEFrameEnvelope(frame)
		trimmedPayload := bytes.TrimSpace(payload)
		replacement := ""
		if json.Valid(trimmedPayload) {
			replacement, _ = SanitizeOpsErrorBodyForQueue(string(trimmedPayload))
		} else if len(trimmedPayload) > 0 && (trimmedPayload[0] == '{' || trimmedPayload[0] == '[') {
			// Replace truncated JSON fragments with a fixed marker so bounded capture
			// cannot persist unredacted partial credentials.
			replacement = `{"payload_truncated":true}`
		}
		if replacement == "" {
			_, _ = out.Write(frame)
			continue
		}
		wroteData := false
		emittedLine := false
		for line := range bytes.SplitSeq(frame, []byte{'\n'}) {
			field, _, found := bytes.Cut(line, []byte{':'})
			if found && bytes.Equal(bytes.TrimSpace(field), []byte("data")) {
				if wroteData {
					continue
				}
				line = append([]byte("data: "), replacement...)
				wroteData = true
			}
			if emittedLine {
				_ = out.WriteByte('\n')
			}
			_, _ = out.Write(line)
			emittedLine = true
		}
	}
	return out.String()
}

// InferStreamFailureStatus selects a logical HTTP failure status from parsed
// terminal stream metadata. Explicit 400..599 statuses win before recognized
// error codes and error types, and unrecognized metadata defaults to 502.
func InferStreamFailureStatus(parsed ParsedOpsError) int {
	if parsed.StatusCode >= 400 && parsed.StatusCode <= 599 {
		return parsed.StatusCode
	}
	switch strings.TrimSpace(parsed.Code) {
	case "rate_limit_exceeded":
		return 429
	case "permission_denied", "permission_error", "insufficient_permissions", "cyber_policy", "content_policy":
		return 403
	case "invalid_request", "context_length_exceeded":
		return 400
	case "server_is_overloaded":
		return 503
	case "service_unavailable", "service_unavailable_error", "server_error":
		return 503
	case "authentication_failed":
		return 401
	}

	switch strings.TrimSpace(parsed.ErrorType) {
	case "rate_limit_error":
		return 429
	case "permission_error", "forbidden_error":
		return 403
	case "authentication_error":
		return 401
	case "invalid_request_error":
		return 400
	case "overloaded_error", "service_unavailable_error":
		return 503
	}

	return 502
}

// ShouldSkipOpsErrorLog reports whether settings suppress queueing an error log
// for the supplied message, body, and requestPath.
func ShouldSkipOpsErrorLog(settings OpsAdvancedSettings, message, body, requestPath string) bool {
	msgLower := strings.ToLower(message)
	bodyLower := strings.ToLower(body)

	if settings.IgnoreCountTokensErrors && strings.Contains(requestPath, "/count_tokens") {
		return true
	}
	if settings.IgnoreContextCanceled &&
		(strings.Contains(msgLower, opsErrContextCanceled) || strings.Contains(bodyLower, opsErrContextCanceled)) {
		return true
	}
	if settings.IgnoreNoAvailableProviders &&
		(strings.Contains(msgLower, opsErrNoAvailableProviders) || strings.Contains(bodyLower, opsErrNoAvailableProviders)) {
		return true
	}
	if settings.IgnoreInvalidApiKeyErrors &&
		(strings.Contains(bodyLower, opsErrInvalidAPIKey) || strings.Contains(bodyLower, opsErrAPIKeyRequired)) {
		return true
	}
	if settings.IgnoreInsufficientBalanceErrors &&
		(strings.Contains(bodyLower, opsErrInsufficientBalance) ||
			strings.Contains(bodyLower, opsErrInsufficientProviderBalance) ||
			strings.Contains(bodyLower, opsErrInsufficientQuota) ||
			strings.Contains(msgLower, opsErrInsufficientBalance) ||
			strings.Contains(msgLower, opsErrInsufficientProviderBalance)) {
		return true
	}

	return false
}

// ShouldSkipMonitoringFromUpstreamEvents reports whether monitoring is
// suppressed by an explicit request flag or by the last non-nil upstream
// attempt event. Using the last event prevents an earlier recovered failover
// attempt's rule from hiding a later visible failure.
func ShouldSkipMonitoringFromUpstreamEvents(requestSkip bool, events []*OpsUpstreamErrorEvent) bool {
	if requestSkip {
		return true
	}
	for _, event := range slices.Backward(events) {
		if event != nil {
			return event.SkipMonitoring
		}
	}
	return false
}

// opsJSONScalarString converts supported JSON string or integer error codes to
// trimmed text, returning empty text for unsupported types.
func opsJSONScalarString(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.Itoa(int(value))
	case json.Number:
		return strings.TrimSpace(value.String())
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	default:
		return ""
	}
}

// opsJSONInt converts supported JSON status fields into int values without
// HTTP-range filtering, leaving 400..599 validation to InferStreamFailureStatus.
func opsJSONInt(value any) int {
	switch value := value.(type) {
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := strconv.Atoi(value.String())
		return parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	case int:
		return value
	case int64:
		return int(value)
	default:
		return 0
	}
}

// opsSSEErrorObject extracts nested error, nested response.error, or flattened
// type=="error" maps from a decoded SSE JSON event.
func opsSSEErrorObject(event map[string]any) map[string]any {
	if event == nil {
		return nil
	}
	if errObj, ok := event["error"].(map[string]any); ok {
		return errObj
	}
	if response, ok := event["response"].(map[string]any); ok {
		if errObj, ok := response["error"].(map[string]any); ok {
			return errObj
		}
	}
	if eventType, _ := event["type"].(string); eventType == "error" {
		return event
	}
	return nil
}

// inferResponsesFailedOpsErrorType maps recognized upstream error codes to
// canonical Ops error categories when an SSE payload omits error.type.
func inferResponsesFailedOpsErrorType(code string) string {
	switch strings.TrimSpace(code) {
	case "rate_limit_exceeded":
		return "rate_limit_error"
	case "permission_denied", "permission_error", "insufficient_permissions", "cyber_policy", "content_policy":
		return "permission_error"
	case "invalid_request", "context_length_exceeded":
		return "invalid_request_error"
	case "server_is_overloaded":
		return "overloaded_error"
	case "service_unavailable", "service_unavailable_error", "server_error":
		return "service_unavailable_error"
	case "authentication_failed":
		return "authentication_error"
	default:
		return ""
	}
}
