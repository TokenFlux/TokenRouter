package middleware

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// request_logger.go creates local request identities and captures request
// completion for observers.
// Every HTTP entry request receives a locally generated diagnostic ID even when
// the caller supplies its own correlation header. The incoming X-Request-ID and
// validated X-Client-Request-ID become searchable caller aliases, while the
// local ID binds response headers, structured logging, and the request capture.
//
// Handlers and upstream header copying can replace response-header values
// before commitment. The writer reapplies its local identities before ordinary
// writes and flushes; a hijacked protocol owns its separately serialized
// handshake headers. Request capture begins before downstream execution and
// finishes in a defer so propagated panics still receive a completion
// observation before they are rethrown to outer recovery.

// requestIDHeader names the local response identifier and the incoming caller
// alias retained for request capture.
const requestIDHeader = "X-Request-ID"

// requestIDWriter reapplies entry correlation values before delegating Gin
// response operations. Callers serialize writes with the request's underlying
// Gin writer, and separately serialized upgrade handshakes supply their own
// response headers.
type requestIDWriter struct {
	// ResponseWriter, id, and clientID belong to one entry request. Delegated
	// writes use id for local diagnostic names and clientID for caller
	// correlation, keeping later header copying from changing those identities.
	gin.ResponseWriter        // wrapped Gin response writer
	id                 string // generated local request ID
	// clientID retains a validated caller correlation value. An empty value
	// selects id when protect rebuilds the X-Client-Request-ID response header.
	clientID string // validated caller correlation ID
}

// protect writes the entry's correlation values into the mutable
// response-header map. The current and legacy internal names use id;
// X-Client-Request-ID uses clientID when present and id otherwise. Changes
// after commitment do not rewrite headers already sent.
func (w *requestIDWriter) protect() {
	w.Header().Set(requestIDHeader, w.id)
	clientID := w.clientID
	if clientID == "" {
		clientID = w.id
	}
	w.Header().Set(clientRequestIDHeader, clientID)
	w.Header().Set(internalRequestIDHeader, w.id)
	w.Header().Set(legacyInternalRequestIDHeader, w.id)
}

// WriteHeader restores correlation values before delegating status selection to
// Gin's writer. Gin can still defer actual commitment until a body write,
// explicit WriteHeaderNow, or flush, so later header copying needs those
// protected entry points.
func (w *requestIDWriter) WriteHeader(code int) {
	w.protect()
	w.ResponseWriter.WriteHeader(code)
}

// WriteHeaderNow restores the entry correlation headers before asking Gin to
// commit its selected status. Repeated commits follow the wrapped writer's
// behavior, while protected header values are reapplied to the mutable map on
// each call.
func (w *requestIDWriter) WriteHeaderNow() {
	w.protect()
	w.ResponseWriter.WriteHeaderNow()
}

// Write restores entry correlation headers before delegating response bytes to
// Gin. The wrapped writer commits its selected status when needed and supplies
// the returned byte count and error to the caller.
func (w *requestIDWriter) Write(data []byte) (int, error) {
	w.protect()
	return w.ResponseWriter.Write(data)
}

// WriteString restores entry correlation headers before delegating text to
// Gin's response writer. The delegated write commits the selected status when
// needed and returns its own byte count and error.
func (w *requestIDWriter) WriteString(data string) (int, error) {
	w.protect()
	return w.ResponseWriter.WriteString(data)
}

// Flush reapplies entry correlation headers before delegating stream flushing
// to Gin. The first flush can commit the selected response status; later
// header-map restoration cannot change correlation headers already sent to the
// client.
func (w *requestIDWriter) Flush() {
	w.protect()
	w.ResponseWriter.Flush()
}

// Hijack reapplies correlation values to the header map and delegates
// connection ownership transfer to Gin. A successful caller owns the returned
// connection and closes it after use. Protocol upgraders that serialize raw
// HTTP 101 responses over the hijacked buffer bypass later writer flushes, so
// include intended handshake headers in the upgrade options.
func (w *requestIDWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.protect()
	return w.ResponseWriter.Hijack()
}

// Unwrap exposes the wrapped writer for http.ResponseController to traverse its
// capabilities. Callers that write directly through the returned writer bypass
// this wrapper's correlation restoration and should use the wrapper for
// ordinary response output.
func (w *requestIDWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// RequestLogger gives each HTTP entry a fresh local ID, request logger, and
// running request capture before invoking downstream handlers. Valid incoming
// correlation IDs become caller aliases, and the first supplied observer is
// used; additional observers are ignored. A nil Request passes through.
//
// Install it before gateway identity middleware so both use the same telemetry
// key. The writer restores local headers at ordinary response commitment, while
// hijacked handshakes provide their own serialized headers. The observer runs
// synchronously under capture locking and must not reenter capture updates;
// shared snapshot timestamp pointers remain read-only.
//
// Completion records Gin's selected status and elapsed milliseconds. A
// still-running record becomes completed, HTTP-failed, or canceled. Propagated
// panics mark the capture failed and set its recorded status to 500 when Gin
// still reports the writer unwritten, before rethrowing. Rejection markers
// replace the error code, and a nonempty client-model observation takes
// precedence over the execution model.
// @project-doc docs/operations/request_lookup.md#request_identity
func RequestLogger(observers ...func(telemetry.RequestRecord)) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil {
			c.Next()
			return
		}

		// Incoming headers can describe callers but cannot choose this capture's
		// storage identity. Create the local UUID before attaching writer, logging,
		// and alias observations.
		requestID := telemetry.NewRequestID()
		writer := &requestIDWriter{ResponseWriter: c.Writer, id: requestID}
		c.Writer = writer
		writer.protect()
		callerID, validCaller := normalizeCorrelationID(c.GetHeader(clientRequestIDHeader))
		if validCaller {
			writer.clientID = callerID
			c.Header(clientRequestIDHeader, callerID)
		} else {
			c.Header(clientRequestIDHeader, requestID)
		}

		ctx := context.WithValue(c.Request.Context(), telemetry.RequestID, requestID)
		parentID, _ := normalizeCorrelationID(c.GetHeader(requestIDHeader))
		if parentID != "" {
			ctx = context.WithValue(ctx, telemetry.ParentRequestID, parentID)
		}

		requestLogger := logging.With(
			zap.String("component", "http"),
			zap.String("request_id", requestID),
			zap.String("parent_request_id", parentID),
			zap.String("path", c.Request.URL.Path),
			zap.String("method", c.Request.Method),
		)

		ctx = logging.IntoContext(ctx, requestLogger)
		started := time.Now()
		ctx = context.WithValue(ctx, telemetry.RequestStartedAt, started)
		var observe func(telemetry.RequestRecord)
		if len(observers) > 0 {
			observe = observers[0]
		}
		path := c.FullPath()
		// Unknown routes have no matched Gin pattern. Capture their requested path so
		// rejected or missing endpoints still produce a diagnosable request summary.
		if path == "" {
			path = c.Request.URL.Path
		}
		record := telemetry.RequestRecord{RequestID: requestID, StartedAt: started, Method: c.Request.Method, Path: path, State: "running"}
		if parentID != "" {
			record.Aliases = append(record.Aliases, telemetry.RequestAlias{Kind: "caller", Value: parentID, Source: requestIDHeader})
		}
		if clientID, valid := normalizeCorrelationID(c.GetHeader(clientRequestIDHeader)); valid {
			record.Aliases = append(record.Aliases, telemetry.RequestAlias{Kind: "caller", Value: clientID, Source: clientRequestIDHeader})
		}
		// Publish the initial running snapshot before downstream updates begin. Later
		// context copies retain the same capture, so observations enrich this entry's
		// request record.
		ctx = telemetry.WithRequestCapture(ctx, record, observe)
		c.Request = c.Request.WithContext(ctx)
		// Finalize the original capture even when downstream code propagates a panic.
		// Restore header values, record completion, then rethrow so outer recovery
		// can write its response.
		defer func() {
			recovered := recover()
			writer.protect()
			telemetry.UpdateRequest(ctx, func(record *telemetry.RequestRecord) {
				finished := time.Now()
				if record.ClientDisconnectedAt != nil && record.ClientDisconnectedAt.Before(finished) {
					finished = *record.ClientDisconnectedAt
				}
				utcFinished := finished.UTC()
				record.FinishedAt = &utcFinished
				// Lifecycle duration uses whole milliseconds, truncating submillisecond
				// elapsed time.
				record.DurationMs = finished.Sub(started).Milliseconds()
				record.Status = c.Writer.Status()
				// A propagated panic decides the failed state before ordinary status and
				// cancellation classification. Substitute 500 only while the response is
				// still unwritten.
				if recovered != nil {
					if !writer.Written() {
						record.Status = http.StatusInternalServerError
					}
					record.State = "failed"
					record.ErrorCode = "panic"
				}
				// Classify the entry's still-running state from response status and
				// cancellation. Keep a state already supplied by downstream observation
				// unless panic handling has replaced it.
				if record.State == "running" {
					record.State = "completed"
					if record.Status >= 400 {
						record.State = "failed"
						if record.ErrorCode == "" {
							record.ErrorCode = "http_" + strconv.Itoa(record.Status)
						}
					}
					// Canceled handling supersedes the ordinary HTTP completion classification
					// in this branch. The propagated-panic state has already bypassed this
					// still-running classification.
					if c.Request.Context().Err() != nil {
						record.State = "canceled"
					}
				}
				if reason, rejected := GetIngressRejectReason(c); rejected {
					record.ErrorCode = string(reason)
				}
				if value, ok := c.Request.Context().Value(telemetry.Model).(string); ok {
					record.Model = value
				}
				// A client display model identifies the caller's requested alias after
				// model rewriting. Prefer that nonempty observation over the execution
				// model in this entry summary.
				if value, ok := c.Request.Context().Value(telemetry.ClientModel).(string); ok && value != "" {
					record.Model = value
				}
				if value, ok := c.Request.Context().Value(telemetry.Platform).(string); ok {
					record.Platform = value
				}
				if value, ok := c.Request.Context().Value(telemetry.ProviderID).(int64); ok {
					record.ProviderID = value
				}
			})
			if recovered != nil {
				panic(recovered)
			}
		}()
		c.Next()
	}
}
