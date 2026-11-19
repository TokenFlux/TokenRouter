package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// search_handler.go admits standalone Web and X search requests, executes
// searches and writes their completion results.
// Standalone Web and X search share a JSON entry flow that rejects malformed
// or blank input before authentication. The request type name appears in JSON
// type errors, while trimmed query selection and a separately normalized
// result limit supply moderation, execution and client response data. XSearch
// selects the X tool on the same Gin context and enters the request activity
// barrier again through WebSearch.
//
// Funding precedes moderation and selection. The request lease retains
// successful provider capacity through synchronous completion capture and
// JSON output, then releases on handler exit. The search loop can release
// replayable failed attempts earlier before selecting another candidate.
// Stage-labeled failures distinguish scheduling and concurrency presentation
// from other search errors, preserving the response order callers and tests
// observe.

// grokStandaloneSearchRequest names JSON field type failures for the HTTP
// entry before conversion to the standalone execution request.
type grokStandaloneSearchRequest searchtools.StandaloneRequest

// SearchAccess carries authenticated group eligibility and optional selection
// identity to WebSearch.
type SearchAccess struct {
	// GroupPresent and Platform determine initial search eligibility; GroupID
	// supplies selection identity only after funding and moderation. WebSearch
	// checks both stages independently, so group presence alone does not admit a
	// missing group identifier.
	GroupPresent bool   // authenticated group object present
	Platform     string // standalone search eligibility platform
	// GroupID borrows the Key's optional group pointer in SearchPorts.Access.
	// Keep that Key stable until WebSearch reads the identifier for selection.
	GroupID *int64 // optional request group identifier
}

// SearchHTTPFailure supplies one admission rejection and its optional retry
// timing to HTTP output.
type SearchHTTPFailure struct {
	Status int // client HTTP rejection status
	// Code: client error category. Message: client error explanation.
	Code, Message string
	// RetryAfter supplies integer seconds for the Retry-After header when
	// positive. WebSearch omits that header for zero or negative values.
	RetryAfter int // seconds
}

// SearchHTTPRun combines selected-provider search execution with synchronous
// completion capture before request cleanup.
type SearchHTTPRun interface {
	// StandalonePorts runs selection, admission and execution under the
	// caller-owned request lease; successful capacity remains available until
	// the handler finishes completion and output.
	searchtools.StandalonePorts
	// Complete captures a successful standalone search for recording before its
	// provider lease is released. WebSearch calls it once after successful
	// RunStandalone, with the same request context and tool mode used for
	// execution.
	//
	// Implementations read mutable HTTP, Key and selected-provider data
	// synchronously before submitting work that can outlive the request. The
	// current SearchPorts run records one search operation from its retained
	// target and query, while the handler presents the supplied execution result
	// separately.
	Complete(*gin.Context, searchtools.StandaloneRequest, searchtools.StandaloneResult, bool)
}

// SearchHTTPPorts supplies admission, platform search execution and
// stage-specific presentation to the standalone handler.
type SearchHTTPPorts interface {
	// DefaultModel supplies the current standalone search model before access
	// checks run. SearchPorts reads the Grok runtime default and uses its fixed
	// text model when that setting is blank.
	DefaultModel() string
	// NormalizeMaxResults resolves the client count into a supported output
	// limit. The Grok adapter uses five for nonpositive input and caps positive
	// input at twenty; the handler retains the raw optional request field
	// separately.
	NormalizeMaxResults(int) int
	// Access supplies group eligibility from authenticated request data and
	// reports whether a Key exists. WebSearch rejects false before funding and
	// checks GroupID separately before selected-provider execution.
	Access(*gin.Context) (SearchAccess, bool)
	// Billing checks request funding before moderation or provider selection.
	// Nil admits the request; a returned failure supplies HTTP status, error
	// text and optional positive Retry-After seconds.
	Billing(*gin.Context) *SearchHTTPFailure
	// Moderate checks the normalized query in an OpenAI Chat audit body for the
	// supplied model. Nil permits execution; a blocked decision supplies client
	// failure details before group selection begins.
	Moderate(*gin.Context, string, []byte) *SearchHTTPFailure
	// Run creates request-local selection and execution state for the
	// authenticated group and Web or X mode. Keep that state on the synchronous
	// request sequence through successful completion capture.
	Run(*gin.Context, int64, bool) SearchHTTPRun
	// ConcurrencyError presents provider admission failure from the search loop.
	// SearchPorts uses the nonstreaming Anthropic error writer; the handler
	// leaves this stage to the adapter rather than its ordinary search JSON
	// wrapper.
	ConcurrencyError(*gin.Context, error)
}

// SearchHandler applies shared admission and selected-provider completion to
// standalone Web and X requests.
type SearchHandler struct {
	RequestLifetime // shared request activity barrier
	// ports retains externally assembled search services. Bind usable
	// dependencies before serving requests; handlers share them while Run
	// supplies per-request selected-target state.
	ports SearchHTTPPorts
}

// NewSearchHandler binds the supplied standalone search dependencies to HTTP
// handling. Construction performs no admission or activity registration;
// application assembly can bind RequestLifetime before exposing the handler.
func NewSearchHandler(ports SearchHTTPPorts) *SearchHandler { return &SearchHandler{ports: ports} }

// XSearch marks the request for native X search and delegates to WebSearch.
// An accepted outer activity entry is released on return; WebSearch performs
// a second entry and its own cleanup on the same Gin context.
func (h *SearchHandler) XSearch(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	// Keep the endpoint choice on the shared request before delegation, so
	// WebSearch selects X request construction and matching completion labels.
	c.Set("grok_x_search_endpoint", true)
	h.WebSearch(c)
}

// searchError writes the standalone search error category and message as
// JSON.
func searchError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
}

// WebSearch admits and executes a standalone search, then captures completion
// before presenting normalized query and results. Callers supply a usable Gin
// request and assembled search dependencies. Malformed JSON and blank input
// fail before authentication; query takes precedence over input, and a
// separate resolved count leaves request field presence available to native
// builders.
//
// Funding rejection precedes moderation, while missing group identity is
// checked after those admission steps. The request lease owns accepted
// provider capacity until completion capture and output finish. Search-loop
// replay can release failed attempts earlier; stage-labeled selection and
// concurrency failures choose their specific presentation. Other failures
// return search-error JSON and release retained request resources on exit.
func (h *SearchHandler) WebSearch(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	isX := c.GetBool("grok_x_search_endpoint")
	if c.Request == nil || c.Request.Body == nil {
		searchError(c, 400, "invalid_request_error", "invalid request")
		return
	}
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		searchError(c, 400, "invalid_request_error", err.Error())
		return
	}
	var decoded grokStandaloneSearchRequest
	if err := json.Unmarshal(rawBody, &decoded); err != nil {
		searchError(c, 400, "invalid_request_error", err.Error())
		return
	}
	if err := wirejson.ValidateRequestObject(rawBody); err != nil {
		searchError(c, 400, "invalid_request_error", err.Error())
		return
	}
	request := searchtools.StandaloneRequest(decoded)
	query := strings.TrimSpace(request.Query)
	// An empty trimmed query permits the alternate input spelling. Keep the
	// canonical query separate from the retained native option fields used by
	// platform builders.
	if query == "" {
		query = strings.TrimSpace(request.Input)
	}
	if query == "" {
		searchError(c, 400, "invalid_request_error", "query is required")
		return
	}
	request.Query = query
	maxResults := 0
	if request.MaxResults != nil {
		maxResults = *request.MaxResults
	}
	maxResults = h.ports.NormalizeMaxResults(maxResults)
	model := h.ports.DefaultModel()
	label := "web_search"
	if isX {
		label = "x_search"
	}
	access, ok := h.ports.Access(c)
	if !ok {
		searchError(c, 401, "authentication_error", "API key required")
		return
	}
	if !access.GroupPresent || access.Platform != "grok" {
		searchError(c, 400, "invalid_request_error", label+" is only supported for grok groups")
		return
	}
	if failure := h.ports.Billing(c); failure != nil {
		if failure.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(failure.RetryAfter))
		}
		searchError(c, failure.Status, failure.Code, failure.Message)
		return
	}
	// The audit shape contains only fixed maps, slices and strings, so
	// marshaling has no unsupported value to reject. Use the normalized query in
	// Chat form for the shared moderation reader.
	auditBody, _ := json.Marshal(map[string]any{"messages": []map[string]any{{"role": "user", "content": query}}})
	if failure := h.ports.Moderate(c, model, auditBody); failure != nil {
		searchError(c, failure.Status, failure.Code, failure.Message)
		return
	}
	if access.GroupID == nil {
		searchError(c, 400, "invalid_request_error", "group required")
		return
	}
	// Attach accepted provider releases to this request lease before running
	// search. Defer its release until successful completion capture and client
	// output have consumed selected-target data.
	lease := scheduler.NewLease(c.Request.Context(), scheduler.ReleaseOnCompletion)
	defer lease.Release()
	run := h.ports.Run(c, *access.GroupID, isX)
	result, err := searchtools.RunStandalone(c.Request.Context(), request, model, maxResults, run, lease)
	if err != nil {
		if failure, ok := errors.AsType[*searchtools.StandaloneFailure](err); ok {
			switch failure.Stage {
			case "selection":
				searchError(c, 503, "scheduling_error", failure.Error())
				return
			case "concurrency":
				h.ports.ConcurrencyError(c, failure.Cause)
				return
			}
		}
		searchError(c, 502, "web_search_error", err.Error())
		return
	}
	// Capture completion while the successful provider lease is still retained,
	// so queued recording receives selected-provider and request observations
	// before handler cleanup releases capacity.
	run.Complete(c, request, result, isX)
	c.JSON(http.StatusOK, gin.H{"query": request.Query, "results": result.Response.Results, "provider": result.Provider, "max_results": maxResults})
}
