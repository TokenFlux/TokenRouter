package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/internal/callerctx"
)

// http.go carries authenticated caller metadata into ordinary Go handlers and
// writes consistent JSON responses for host capability failures. Credentials
// remain private fields populated only by the RPC adapter; plugins consume a
// caller value without needing to extract or forward its grant themselves.

const (
	// HTTP response buffering stays below the host transport limit while retaining one overflow byte for explicit size rejection.
	maxResponseBytes = 4<<20 + 1
)

// Caller holds host-verified identity and private request authorization metadata.
type Caller struct {
	UserID   int64  // verified user identifier
	Username string // verified account display name
	Role     string // verified account role
	Locale   string // negotiated request language
	grant    string // host-signed caller authorization ticket
	clientIP string // host-resolved client IP address
}

// responseBuffer implements a bounded nonstreaming response writer for RPC.
type responseBuffer struct {
	header http.Header // handler response header values
	status int         // committed HTTP status code
	body   []byte      // bounded response body bytes
}

// CallerFrom retrieves host-provided identity from an SDK HTTP handler context.
// Public requests may contain a caller with UserID zero. A false result means
// the context did not originate from the SDK request adapter.
func CallerFrom(ctx context.Context) (Caller, bool) {
	identity, ok := callerctx.From(ctx)
	return Caller{UserID: identity.UserID, Username: identity.Username, Role: identity.Role, Locale: identity.Locale, grant: identity.Grant, clientIP: identity.ClientIP}, ok
}

// WriteJSON writes one JSON value and the requested HTTP response status.
// Encoding occurs before headers are committed, so unsupported values produce
// a plain internal-error JSON response instead of partially encoded output.
func WriteJSON(w http.ResponseWriter, statusCode int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		statusCode = http.StatusInternalServerError
		data = []byte(`{"code":"ENCODING_FAILED","message":"Response encoding failed"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = w.Write(data)
}

// WriteError maps host failures to safe HTTP status and reason values.
// Unknown internal errors are not exposed to users. Plugin authors can log the
// original error separately when their diagnostics policy permits its contents.
func WriteError(w http.ResponseWriter, err error) {
	statusCode, reason := http.StatusInternalServerError, "INTERNAL_ERROR"
	switch {
	case errors.Is(err, ErrPermissionDenied):
		statusCode, reason = http.StatusForbidden, "PERMISSION_NOT_GRANTED"
	case errors.Is(err, ErrGrantInvalid):
		statusCode, reason = http.StatusForbidden, "GRANT_INVALID"
	case errors.Is(err, ErrInsufficientBalance):
		statusCode, reason = http.StatusConflict, "INSUFFICIENT_BALANCE"
	case errors.Is(err, ErrVersionConflict):
		statusCode, reason = http.StatusConflict, "VERSION_CONFLICT"
	case errors.Is(err, ErrUserNotFound):
		statusCode, reason = http.StatusNotFound, "USER_NOT_FOUND"
	case errors.Is(err, ErrLimitExceeded):
		statusCode, reason = http.StatusTooManyRequests, "LIMIT_EXCEEDED"
	case errors.Is(err, ErrInvalidArgument):
		statusCode, reason = http.StatusBadRequest, "INVALID_ARGUMENT"
	case errors.Is(err, ErrPlanNotAllowed):
		statusCode, reason = http.StatusForbidden, "PLAN_NOT_ALLOWED"
	case errors.Is(err, ErrHostUnavailable):
		statusCode, reason = http.StatusServiceUnavailable, "HOST_UNAVAILABLE"
	case errors.Is(err, ErrEmailDeliveryUnknown):
		statusCode, reason = http.StatusConflict, "EMAIL_DELIVERY_UNKNOWN"
	}
	WriteJSON(w, statusCode, map[string]string{"code": reason})
}

// Header returns the mutable headers used by this individual response.
func (w *responseBuffer) Header() http.Header { return w.header }

// WriteHeader records the first final HTTP status supplied by the handler.
// Informational statuses are ignored because the plugin protocol carries one
// complete response. Later final status writes do not replace the first status.
func (w *responseBuffer) WriteHeader(code int) {
	if code < 100 || code > 999 {
		panic("invalid HTTP status code")
	}
	if code < 200 || w.status != 0 {
		return
	}
	w.status = code
}

// Write appends response data up to the host body limit plus one byte.
// Overflow returns an error and retains enough data for host rejection;
// handlers cannot force the SDK to buffer an unbounded response.
func (w *responseBuffer) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	remaining := maxResponseBytes - len(w.body)
	n := min(len(data), remaining)
	w.body = append(w.body, data[:n]...)
	if n < len(data) {
		return n, fmt.Errorf("plugin response exceeds body limit")
	}
	return n, nil
}
