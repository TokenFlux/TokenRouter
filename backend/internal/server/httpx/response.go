package httpx

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

// response.go writes panel response envelopes and resolves pagination
// parameters for HTTP handlers.
// Panel responses pair an HTTP status with an envelope code: success writers
// use zero, while error writers use the supplied status value. JSON encoding
// keeps supplied payload shapes, including omission for a nil data interface.
// Typed nil payloads can encode as null. Mutable payloads remain stable while
// Gin synchronously encodes them.
//
// Error text follows request-language choices outside the administrator and
// setup path prefixes. Pagination has a separate precedence rule: a nonempty
// page_size selects its branch even when unusable, so a valid limit alias cannot
// replace that value. Missing or rejected values retain defaults, and supplied
// pagination results pass through to the response while calculated results use
// floating-point ceiling division with at least one page.

// Response is the numeric-code envelope written by panel API handlers.
type Response struct {
	Code    int    `json:"code"`             // zero success or HTTP error status
	Message string `json:"message"`          // response display message text
	Reason  string `json:"reason,omitempty"` // optional stable error reason
	// Metadata is serialized as supplied by ErrorWithDetails. ErrorFrom obtains
	// an independent string-map copy through ToHTTP; direct callers keep their
	// supplied map stable until encoding finishes.
	Metadata map[string]string `json:"metadata,omitempty"`
	// Data retains the writer's payload. A nil interface is omitted, while a
	// typed nil can encode as null; callers keep mutable references stable during encoding.
	Data any `json:"data,omitempty"`
}

// PaginatedData is the item collection and navigation counts inside a panel success envelope.
type PaginatedData struct {
	// Items is encoded as supplied, including null for a nil value. Keep the
	// mutable collection stable until the page writer finishes its JSON encoding.
	Items    any   `json:"items"`
	Total    int64 `json:"total"`     // total matching item count
	Page     int   `json:"page"`      // requested current page number
	PageSize int   `json:"page_size"` // items per requested page
	Pages    int   `json:"pages"`     // total available page count
}

// PaginationResult carries caller-computed totals and page counts for panel response writers.
type PaginationResult struct {
	// Total, Page, PageSize, and Pages are copied directly by PaginatedWithResult.
	// Callers supply normalized page values and their computed counts; that writer
	// provides defaults only when the entire pagination pointer is nil.
	Total    int64 // total matching item count
	Page     int   // supplied current page number
	PageSize int   // supplied items-per-page value
	Pages    int   // caller-computed total page count
}

// Success writes HTTP 200 with envelope code zero and message success.
// Supply JSON-encodable data and a usable Gin writer; nil interface data is
// omitted, and mutable payloads remain stable until this synchronous encoding finishes.
func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// Created writes HTTP 201 with envelope code zero and message success.
// Supply JSON-encodable creation data; a nil data interface is omitted from the
// envelope. Mutable payloads remain stable while Gin completes the response encoding.
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// Accepted writes HTTP 202 with envelope code zero and message accepted.
// Supply JSON-encodable acceptance data; a nil interface omits the payload.
// Callers keep mutable values stable until the synchronous response encoding returns.
func Accepted(c *gin.Context, data any) {
	c.JSON(http.StatusAccepted, Response{
		Code:    0,
		Message: "accepted",
		Data:    data,
	})
}

// Error writes the supplied HTTP status and message through the detailed error writer.
// Reason and metadata start empty; ErrorWithDetails applies its path-language
// rules. Callers return from normal handler processing after choosing this response.
func Error(c *gin.Context, statusCode int, message string) {
	ErrorWithDetails(c, statusCode, message, "", nil)
}

// ErrorWithDetails writes a numeric-status envelope with optional reason and metadata.
// A populated request outside /api/v1/admin/ and /setup/ prefixes receives
// request-language error text, with HTTP_n synthesized for an empty reason.
// Nil requests and those prefixes retain supplied text and reason. Metadata is
// encoded directly, and callers control handler return after the response.
func ErrorWithDetails(c *gin.Context, statusCode int, message, reason string, metadata map[string]string) {
	if c.Request != nil && !strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/") && !strings.HasPrefix(c.Request.URL.Path, "/setup/") {
		// Add a status-based reason when the owner supplied none, giving clients
		// a stable identifier alongside the message selected for the request language.
		if reason == "" {
			reason = "HTTP_" + strconv.Itoa(statusCode)
		}
		language, exists := locale.Explicit(c.Request.Context())
		if !exists {
			language = locale.Negotiate(c.GetHeader("Accept-Language"), locale.Default())
		}
		message = locale.ErrorText(language, reason, statusCode, message)
	}
	c.JSON(statusCode, Response{
		Code:     statusCode,
		Message:  message,
		Reason:   reason,
		Metadata: metadata,
	})
}

// ErrorFrom writes the mapped envelope for a nonnil error and returns true.
// Nil interfaces return false with no response. Server-class codes log redacted
// diagnostics when a request exists; callers return from their normal handler
// path when this helper reports that it wrote the result.
func ErrorFrom(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}

	statusCode, status := ToHTTP(err)

	// The response uses the mapped status, while diagnostics retain the original
	// cause chain. Redact that text before logging request-associated server failures.
	if statusCode >= 500 && c.Request != nil {
		logger.LegacyPrintf("server.httpx", "[ERROR] %s %s\n  Error: %s", c.Request.Method, c.Request.URL.Path, logredact.RedactText(err.Error()))
	}

	ErrorWithDetails(c, statusCode, status.Message, status.Reason, status.Metadata)
	return true
}

// BadRequest writes HTTP 400 with the supplied invalid-request message.
// The caller determines the validation failure and returns after choosing this
// response; ErrorWithDetails supplies the applicable path-language rules and optional status reason.
func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, message)
}

// Unauthorized writes HTTP 401 for the caller's rejected authentication result.
// Callers return from that handler path after writing; the common error writer
// supplies applicable request-language text and a status-based reason for the message.
func Unauthorized(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, message)
}

// Forbidden writes HTTP 403 with the supplied permission-failure message.
// Permission checks belong to the caller, which returns after writing. The
// common error writer applies its request-path and language rules to the response.
func Forbidden(c *gin.Context, message string) {
	Error(c, http.StatusForbidden, message)
}

// NotFound writes HTTP 404 with the supplied missing-resource message.
// Callers return after choosing this response, and ErrorWithDetails selects
// request-language text when a populated request belongs to its localized path scope.
func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, message)
}

// InternalError writes HTTP 500 with the message supplied by its owner.
// Supply text appropriate for clients because this helper passes it to the
// common error writer; ErrorFrom separately provides diagnostic redaction and logging.
func InternalError(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, message)
}

// Paginated writes items and computes the total page count from total and pageSize.
// Supply a positive pageSize. Ceiling division uses float64 and retains at
// least one page; items, total, page, and size pass through, with floating-point
// precision limits applying to very large counts.
func Paginated(c *gin.Context, items any, total int64, page, pageSize int) {
	// Floating-point ceiling division counts partially filled pages. The minimum
	// keeps navigation usable for empty results instead of exposing a zero-page collection.
	pages := max(int(math.Ceil(float64(total)/float64(pageSize))), 1)

	Success(c, PaginatedData{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	})
}

// PaginatedWithResult writes items with the supplied navigation counts.
// Nil pagination selects page one, size twenty, zero total, and one page.
// A nonnil result passes all counts through as given, so its caller owns
// normalization and keeps the result stable during encoding.
func PaginatedWithResult(c *gin.Context, items any, pagination *PaginationResult) {
	if pagination == nil {
		Success(c, PaginatedData{
			Items:    items,
			Total:    0,
			Page:     1,
			PageSize: 20,
			Pages:    1,
		})
		return
	}

	Success(c, PaginatedData{
		Items:    items,
		Total:    pagination.Total,
		Page:     pagination.Page,
		PageSize: pagination.PageSize,
		Pages:    pagination.Pages,
	})
}

// ParsePagination returns page and page-size choices from the request query.
// Defaults are one and twenty. Positive parsed pages are accepted, while size
// choices need a parsed value from one through one thousand; rejected values
// leave the corresponding default in place.
//
// A nonempty page_size wins over limit even when its number is rejected. Empty
// primary values allow the alias to be considered. parseInt reports zero for
// nondigits and performs unchecked native-int accumulation, so this selection
// tests its computed result rather than establishing overflow-free input parsing.
func ParsePagination(c *gin.Context) (page, pageSize int) {
	page = 1
	pageSize = 20

	if p := c.Query("page"); p != "" {
		if val, err := parseInt(p); err == nil && val > 0 {
			page = val
		}
	}

	// Both size branches retain the default for unusable computed numbers,
	// including values above one thousand. A nonempty primary selects its branch
	// even when rejected, keeping that configured priority over a valid limit alias.
	if ps := c.Query("page_size"); ps != "" {
		if val, err := parseInt(ps); err == nil && val > 0 && val <= 1000 {
			pageSize = val
		}
	} else if l := c.Query("limit"); l != "" {
		if val, err := parseInt(l); err == nil && val > 0 && val <= 1000 {
			pageSize = val
		}
	}

	return page, pageSize
}

// parseInt accumulates ASCII decimal digits into a native int without overflow checks.
// Empty or nondigit input returns zero with nil error, and the error result is
// always nil. Pagination callers validate the computed value against their
// positive-page and bounded-size rules.
func parseInt(s string) (int, error) {
	var result int
	for _, c := range s {
		// Nondigits select zero so pagination guards retain their defaults. This
		// helper exposes that value instead of a separate malformed-input diagnostic.
		if c < '0' || c > '9' {
			return 0, nil
		}
		// Native-int decimal accumulation leaves overflow unchecked for this parser.
		result = result*10 + int(c-'0')
	}
	return result, nil
}
