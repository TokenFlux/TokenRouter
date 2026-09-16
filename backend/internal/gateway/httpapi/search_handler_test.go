package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type searchHTTPStub struct {
	calls         []string
	authenticated bool
	platform      string
	billing       *SearchHTTPFailure
	moderation    *SearchHTTPFailure
	isX           bool
	released      bool
	completed     bool
}

func (s *searchHTTPStub) DefaultModel() string {
	s.calls = append(s.calls, "model")
	return "grok-test"
}
func (s *searchHTTPStub) NormalizeMaxResults(n int) int {
	if n <= 0 {
		return 5
	}
	if n > 20 {
		return 20
	}
	return n
}
func (s *searchHTTPStub) Access(*gin.Context) (SearchAccess, bool) {
	s.calls = append(s.calls, "access")
	id := int64(1)
	return SearchAccess{GroupPresent: true, Platform: s.platform, GroupID: &id}, s.authenticated
}
func (s *searchHTTPStub) Billing(*gin.Context) *SearchHTTPFailure {
	s.calls = append(s.calls, "billing")
	return s.billing
}
func (s *searchHTTPStub) Moderate(*gin.Context, string, []byte) *SearchHTTPFailure {
	s.calls = append(s.calls, "moderation")
	return s.moderation
}
func (s *searchHTTPStub) Run(_ *gin.Context, _ int64, isX bool) SearchHTTPRun {
	s.calls = append(s.calls, "run")
	s.isX = isX
	return s
}
func (s *searchHTTPStub) ConcurrencyError(c *gin.Context, err error) {
	c.JSON(429, gin.H{"message": err.Error()})
}
func (s *searchHTTPStub) Select(context.Context, string, map[int64]struct{}) (searchtools.Selection, bool, error) {
	s.calls = append(s.calls, "select")
	return searchtools.Selection{AccountID: 7}, true, nil
}
func (s *searchHTTPStub) Acquire(context.Context, searchtools.Selection) (func(), bool, error) {
	return func() { s.released = true; s.calls = append(s.calls, "release") }, true, nil
}
func (s *searchHTTPStub) Execute(_ context.Context, _ int64, request searchtools.StandaloneRequest, _ string, _ int) (*contract.SearchResponse, string, error) {
	return &contract.SearchResponse{Query: request.Query, Results: []contract.SearchResult{{URL: "https://source.test", Title: "source", Snippet: "snippet"}}}, "grok-native", nil
}
func (s *searchHTTPStub) CanSwitch(error) bool { return false }
func (s *searchHTTPStub) Complete(_ *gin.Context, _ searchtools.StandaloneRequest, _ searchtools.StandaloneResult, _ bool) {
	if s.released {
		panic("account released before completion snapshot")
	}
	s.completed = true
	s.calls = append(s.calls, "complete")
}
func searchContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/web_search", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, r
}
func TestStandaloneSearchHTTPOrderAndResponse(t *testing.T) {
	ports := &searchHTTPStub{authenticated: true, platform: "grok"}
	c, response := searchContext(`{"input":" query ","max_results":99}`)
	NewSearchHandler(ports).XSearch(c)
	require.Equal(t, 200, response.Code)
	require.True(t, ports.isX)
	require.True(t, ports.completed)
	require.True(t, ports.released)
	require.Equal(t, []string{"model", "access", "billing", "moderation", "run", "select", "complete", "release"}, ports.calls)
	require.JSONEq(t, `{"query":"query","results":[{"url":"https://source.test","title":"source","snippet":"snippet"}],"provider":"grok-native","max_results":20}`, response.Body.String())
}
func TestStandaloneSearchHTTPRejectsInOriginalOrder(t *testing.T) {
	t.Run("parse before auth", func(t *testing.T) {
		p := &searchHTTPStub{}
		c, r := searchContext(`{"query":`)
		NewSearchHandler(p).WebSearch(c)
		require.Equal(t, 400, r.Code)
		require.Empty(t, p.calls)
	})
	t.Run("field type error retains old struct name", func(t *testing.T) {
		p := &searchHTTPStub{}
		c, r := searchContext(`{"query":7}`)
		NewSearchHandler(p).WebSearch(c)
		require.Equal(t, 400, r.Code)
		require.JSONEq(t, `{"error":{"type":"invalid_request_error","message":"json: cannot unmarshal number into Go struct field grokStandaloneSearchRequest.query of type string"}}`, r.Body.String())
		require.Empty(t, p.calls)
	})

	t.Run("missing query before auth", func(t *testing.T) {
		p := &searchHTTPStub{}
		c, r := searchContext(`{}`)
		NewSearchHandler(p).WebSearch(c)
		require.Equal(t, 400, r.Code)
		require.Contains(t, r.Body.String(), "query is required")
		require.Empty(t, p.calls)
	})
	t.Run("billing before moderation", func(t *testing.T) {
		p := &searchHTTPStub{authenticated: true, platform: "grok", billing: &SearchHTTPFailure{Status: 429, Code: "quota", Message: "limited", RetryAfter: 3}}
		c, r := searchContext(`{"query":"q"}`)
		NewSearchHandler(p).WebSearch(c)
		require.Equal(t, 429, r.Code)
		require.Equal(t, "3", r.Header().Get("Retry-After"))
		require.Equal(t, []string{"model", "access", "billing"}, p.calls)
	})
}
func TestSearchOutputHeadersAndPerEventFlush(t *testing.T) {
	c, r := searchContext(`{}`)
	out := SearchOutput{Context: c}
	out.StartStream()
	require.NoError(t, out.WriteEvent("message_stop", []byte(`{"type":"message_stop"}`)))
	out.Flush()
	require.Equal(t, 200, r.Code)
	require.Equal(t, "text/event-stream", r.Header().Get("Content-Type"))
	require.Equal(t, "no-cache", r.Header().Get("Cache-Control"))
	require.Equal(t, "no", r.Header().Get("X-Accel-Buffering"))
	require.True(t, r.Flushed)
	require.Equal(t, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", r.Body.String())
}
