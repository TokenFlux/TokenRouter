package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// share_image_test.go exercises public cover requests through registered routes.
// The mutable source represents committed replacement and removal, while explicit
// storage failures verify that errors cannot masquerade as successful image bytes.
// No authentication or third-party image host participates in these requests.

// coverSource provides current cover bytes and controlled storage failures for HTTP tests.
type coverSource struct {
	calendarPublicSource        // empty public settings fixture
	value                string // current encoded cover
	err                  error  // injected storage failure
}

// LoadSiteShareImage returns the fixture's current cover and selected storage error.
func (s *coverSource) LoadSiteShareImage(context.Context) (string, error) { return s.value, s.err }

// TestGetShareImage exercises anonymous GET, HEAD, conditional reads, replacement, and removal.
// Successful bodies retain their image type, while unavailable or obsolete covers never return cacheable success responses.
func TestGetShareImage(t *testing.T) {
	var raw bytes.Buffer
	require.NoError(t, png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 2, 3))))
	source := &coverSource{value: "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw.Bytes())}
	cover, err := site.ParseShareImage(source.value)
	require.NoError(t, err)
	handler := NewPublicHandler(site.NewPublicService(source, timezone.NewCalendar(time.UTC), "UTC"), "test")
	router := gin.New()
	RegisterPublicSettingsRoutes(router.Group("/api/v1/settings"), handler)
	path := "/api/v1/settings/share-image/" + cover.Metadata.Hash
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, 200, get.Code)
	require.Equal(t, raw.Bytes(), get.Body.Bytes())
	require.Equal(t, "image/png", get.Header().Get("Content-Type"))
	head := httptest.NewRecorder()
	router.ServeHTTP(head, httptest.NewRequest(http.MethodHead, path, nil))
	require.Equal(t, 200, head.Code)
	require.Empty(t, head.Body.Bytes())
	require.Equal(t, get.Header().Get("Content-Length"), head.Header().Get("Content-Length"))
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("If-None-Match", get.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	router.ServeHTTP(cached, request)
	require.Equal(t, 304, cached.Code)
	source.err = errors.New("storage unavailable")
	failed := httptest.NewRecorder()
	router.ServeHTTP(failed, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, 500, failed.Code)
	require.Equal(t, "no-store", failed.Header().Get("Cache-Control"))
	source.err = nil
	raw.Reset()
	require.NoError(t, png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 3, 3))))
	source.value = "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw.Bytes())
	for _, value := range []string{source.value, ""} {
		source.value = value
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, 404, response.Code)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
}
