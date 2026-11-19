package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
)

// media_response.go writes media HTTP responses and handles image errors after
// JSON or SSE heartbeat commitment.
// Image errors can arrive after a heartbeat has already committed HTTP status
// 200. JSON whitespace permits a final error document, while SSE comments need
// a terminal error event. The image writer consults heartbeat-adjusted bytes
// before deciding whether an error can still be appended; a reported
// write means presentation was attempted, while underlying delivery remains
// the response writer's result.
//
// Video content uses a small set of download headers, including range and
// length information, before copying the body to the client. Content URLs
// retain the incoming route family and escape a task identifier as one path
// segment so a slash inside that identifier cannot become another route
// component. Voice body reads and expected Realtime closure classification
// serve the same HTTP media entry points.

// ImageErrorResponse carries sanitized image error fields for HTTP JSON before
// stream commitment or an SSE error event afterward.
type ImageErrorResponse struct {
	// Status is requested when writing the error; a heartbeat may already have
	// committed status 200, which the underlying writer retains.
	Status int // requested HTTP status
	// Code and Param are included when their trimmed values are nonempty. Type
	// and Message are supplied by the caller and are written as provided.
	// Type: supplied error category. Message: supplied client message.
	// Code: optional error code. Param: optional parameter name.
	Type, Message, Code, Param string
}

// WriteEmbeddingsUpstreamResponse presents buffered upstream bytes with their
// HTTP status and filtered headers. Nil context or response, or a committed
// writer, skips presentation. Content-Type comes from upstream when present
// and defaults to application/json otherwise; the caller owns the
// already-read response body and its closing.
func WriteEmbeddingsUpstreamResponse(c *gin.Context, resp *http.Response, body []byte, filter *egress.CompiledHeaderFilter) {
	if c == nil || resp == nil {
		return
	}
	if c.Writer.Written() {
		return
	}
	if resp.Header != nil {
		egressprovider.WriteFilteredHeaders(c.Writer.Header(), resp.Header, filter)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Writer.Header().Set("Content-Type", ct)
	} else {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	c.Writer.WriteHeader(resp.StatusCode)
	// Presentation errors stay with the HTTP writer; this adapter has no recovery
	// result to return after committing the upstream status and headers.
	_, _ = c.Writer.Write(body)
}

// WriteEmbeddingsError writes the supplied category and message in an OpenAI
// error object. Callers provide a usable Gin context and decide whether an
// existing response permits another JSON write.
func WriteEmbeddingsError(c *gin.Context, statusCode int, errType, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// WriteGrokMediaErrorResponse writes trimmed error fields when the response
// is still uncommitted. Missing context or writer, or any prior commitment,
// skips presentation so an existing media response stays intact.
func WriteGrokMediaErrorResponse(c *gin.Context, statusCode int, errType, message string) {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    strings.TrimSpace(errType),
			"message": strings.TrimSpace(message),
		},
	})
}

// WriteGrokMediaContentResponse copies a video download with range and
// content headers, returning the copy error. Context, response and response
// body are required. The optional committed callback runs after setting
// status and before copying bytes; the caller closes the upstream body after
// this function returns.
func WriteGrokMediaContentResponse(c *gin.Context, resp *http.Response, committed func()) error {
	if c == nil || resp == nil || resp.Body == nil {
		return fmt.Errorf("grok media content response is incomplete")
	}

	for _, name := range []string{
		"Content-Type",
		"Content-Length",
		"Content-Range",
		"Accept-Ranges",
		"Content-Disposition",
	} {
		if value := strings.TrimSpace(resp.Header.Get(name)); value != "" {
			c.Header(name, value)
		}
	}
	// Explicit download headers take precedence. Supply known transport length
	// and a generic binary type when upstream headers leave those values absent.
	if strings.TrimSpace(c.Writer.Header().Get("Content-Length")) == "" && resp.ContentLength >= 0 {
		c.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if strings.TrimSpace(c.Writer.Header().Get("Content-Type")) == "" {
		c.Header("Content-Type", "application/octet-stream")
	}
	// Notify optional commitment bookkeeping before copying so the owner treats a
	// later read failure as a possibly partial download when choosing terminal handling.
	c.Status(resp.StatusCode)
	if committed != nil {
		committed()
	}
	_, err := io.Copy(c.Writer, resp.Body)
	return err
}

// ReadGrokVoiceBody reads the request body and returns its bytes or read
// error. A missing body is accepted for GET and DELETE; other methods require
// one, and syntactically valid JSON objects with duplicate or case-ambiguous
// fields fail with wirejson.RequestFieldError. The HTTP caller supplies any
// size restriction and owns request cleanup.
func ReadGrokVoiceBody(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil {
		return nil, errors.New("request body is required")
	}
	if c.Request.Body == nil {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodDelete {
			return nil, nil
		}
		return nil, errors.New("request body is required")
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	if err := rejectAmbiguousJSONObject(body); err != nil {
		return nil, err
	}
	return body, nil
}

// IsExpectedGrokRealtimeClose reports whether relay completion permits the
// HTTP Realtime path to retain observed audio usage. Nil and normal,
// going-away, no-status, or abnormal close codes qualify; other codes end
// that completion path.
func IsExpectedGrokRealtimeClose(err error) bool {
	if err == nil {
		return true
	}
	// The Realtime handler records observed audio for these ordinary departure
	// codes, including missing or abnormal close frames after a connection ends.
	switch coderws.CloseStatus(err) {
	case coderws.StatusNormalClosure, coderws.StatusGoingAway,
		coderws.StatusNoStatusRcvd, coderws.StatusAbnormalClosure:
		return true
	default:
		return false
	}
}

// WriteImageError attempts a final image error in JSON or SSE when actual
// response content has not been written. Missing context, writer or value
// returns false.
// Callers provide usable adjustedWritten and stopKeepalive callbacks; true
// reports attempted presentation, because Gin does not return the underlying
// write error.
//
// Heartbeats can commit HTTP status while leaving adjusted output negative.
// That case stops further activity and writes the error in the selected JSON
// or SSE format. A response with nonnegative adjusted output is left intact.
// Code and Param are trimmed and included only when populated; Type and Message
// retain their supplied values.
func WriteImageError(c *gin.Context, value *ImageErrorResponse, adjustedWritten func() int, stopKeepalive func()) bool {
	if c == nil || c.Writer == nil || value == nil {
		return false
	}
	// Stop the shared SSE controller before inspecting bytes or writing errors;
	// a pending beat must not race the decision between HTTP JSON and SSE framing.
	StopOpenAICompactSSEKeepaliveCommitted(c)
	streamCommitted := c.Writer.Written() && strings.Contains(strings.ToLower(c.Writer.Header().Get("Content-Type")), "text/event-stream")
	// Heartbeat-only commitment still permits one final error. The adjusted count
	// prevents appending another terminal response after actual media output.
	if c.Writer.Written() && adjustedWritten() >= 0 {
		return false
	}
	stopKeepalive()
	object := gin.H{"type": value.Type, "message": value.Message}
	if code := strings.TrimSpace(value.Code); code != "" {
		object["code"] = code
	}
	if param := strings.TrimSpace(value.Param); param != "" {
		object["param"] = param
	}
	if streamCommitted {
		payload, err := json.Marshal(gin.H{"error": object})
		if err != nil {
			_ = c.Error(err)
			return false
		}
		// The wire status is already 200. Keep the intended upstream failure in
		// observations and mark terminal presentation before attempting its only frame.
		if c.Request != nil && c.Request.Context().Err() != nil {
			// Upstream work can finish after client departure. Retain its error
			// evidence while letting request finalization keep the canceled lifecycle.
			markOpsStreamError(c, OpsStreamError{ErrType: value.Type, Code: value.Code, Message: value.Message, IntendedStatus: value.Status})
		} else {
			MarkOpsStreamFailure(c, value.Type, value.Code, value.Message, value.Status)
		}
		MarkResponseCommitted(c)
		if _, err := fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload); err != nil {
			_ = c.Error(err)
		}
		c.Writer.Flush()
		return true
	}
	c.JSON(value.Status, gin.H{"error": object})
	return true
}

// GrokMediaContentProxyURL builds a relative download route for a nonblank
// task identifier. Missing request data returns an empty string. Incoming
// paths beginning with /v1/ select that prefix, while other paths select the
// bare video route.
//
// The task identifier loses surrounding slashes and is path-escaped as one
// segment before appending /content. This keeps an internal slash encoded in
// the identifier, allowing generated status responses to refer back to the
// same task through either registered route family.
func GrokMediaContentProxyURL(c *gin.Context, requestID string) string {
	if c == nil || c.Request == nil || c.Request.URL == nil || strings.TrimSpace(requestID) == "" {
		return ""
	}
	pathPrefix := ""
	if strings.HasPrefix(c.Request.URL.Path, "/v1/") {
		pathPrefix = "/v1"
	}
	// Escape the task as one segment before appending the content suffix so IDs
	// containing internal slashes remain task identifiers in the generated route.
	return pathPrefix + "/videos/" + url.PathEscape(strings.Trim(requestID, "/")) + "/content"
}
