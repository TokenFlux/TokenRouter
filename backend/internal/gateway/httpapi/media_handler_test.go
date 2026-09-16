package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mediaHTTPProbe struct {
	AuxiliaryHTTPPorts
	t       *testing.T
	access  *MediaAccess
	steps   []string
	mapping routing.ChannelMappingResult
}

func (p *mediaHTTPProbe) Access(*gin.Context) (*MediaAccess, bool) {
	p.steps = append(p.steps, "auth")
	return p.access, p.access != nil
}
func (p *mediaHTTPProbe) Subject(*gin.Context) (MediaSubject, bool) {
	return MediaSubject{UserID: 1, Concurrency: 1}, true
}
func (p *mediaHTTPProbe) Error(c *gin.Context, status int, kind, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": kind, "message": message}})
}
func (p *mediaHTTPProbe) EnsureForwardError(*gin.Context, bool) bool {
	p.t.Error("unexpected panic in media HTTP adapter")
	return false
}
func (p *mediaHTTPProbe) Logger(*gin.Context, string, ...zap.Field) *zap.Logger { return zap.NewNop() }
func (p *mediaHTTPProbe) Dependencies(*gin.Context, *zap.Logger) bool           { return true }
func (p *mediaHTTPProbe) HTTPTransport(*gin.Context)                            {}
func (p *mediaHTTPProbe) ObserveRequest(*gin.Context, string, bool, bool)       {}
func (p *mediaHTTPProbe) Plan(c *gin.Context, _ string, _ bool) (context.Context, routing.ChannelMappingResult) {
	p.steps = append(p.steps, "plan")
	return c.Request.Context(), p.mapping
}
func (p *mediaHTTPProbe) ImagePolicyDenied(*gin.Context) { p.steps = append(p.steps, "denied") }
func (p *mediaHTTPProbe) ImagePermissionMessage() string { return "image disabled" }
func (p *mediaHTTPProbe) ParseGrok(string, []byte) GrokMediaInput {
	p.steps = append(p.steps, "parse")
	return GrokMediaInput{}
}
func (p *mediaHTTPProbe) NormalizeGrok(string, string, bool) string { return "" }

type unreadableMediaBody struct{ reads int }

func (b *unreadableMediaBody) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("must not read")
}
func (b *unreadableMediaBody) Close() error { return nil }
func mediaHTTPContext(method, path string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, body)
	return c, rec
}
func TestMediaHTTPAuthenticationAndPlatformChecksDoNotReadBody(t *testing.T) {
	for _, kind := range []string{"images", "embeddings", "alpha", "voice"} {
		t.Run(kind, func(t *testing.T) {
			p := &mediaHTTPProbe{t: t}
			body := &unreadableMediaBody{}
			c, rec := mediaHTTPContext(http.MethodPost, "/v1/test", nil)
			c.Request.Body = body
			switch kind {
			case "images":
				NewMediaHandler(p).Images(c)
			case "embeddings":
				NewAuxiliaryHandler(p).Embeddings(c)
			case "alpha":
				NewAuxiliaryHandler(p).AlphaSearch(c)
			case "voice":
				NewAuxiliaryHandler(p).GrokVoice(c, "tts")
			}
			expected := 401
			if kind == "voice" {
				expected = 404
			}
			require.Equal(t, expected, rec.Code)
			require.Zero(t, body.reads)
		})
	}
}
func TestImagesHTTPValidatesMappedModelBeforeFeatureDenial(t *testing.T) {
	p := &mediaHTTPProbe{t: t, access: &MediaAccess{ID: 2}, mapping: routing.ChannelMappingResult{Mapped: true, MappedModel: "gpt-image-2"}}
	c, rec := mediaHTTPContext(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"alias","prompt":"cat"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	NewMediaHandler(p).Images(c)
	require.Equal(t, 403, rec.Code)
	require.Equal(t, []string{"auth", "plan", "denied"}, p.steps)
}
func TestGrokVideoLookupSkipsBodyAndRequiresTaskID(t *testing.T) {
	p := &mediaHTTPProbe{t: t, access: &MediaAccess{ID: 2}}
	body := &unreadableMediaBody{}
	c, rec := mediaHTTPContext(http.MethodGet, "/v1/videos/", nil)
	c.Request.Body = body
	NewMediaHandler(p).GrokMedia(c, "video_status", "")
	require.Equal(t, 400, rec.Code)
	require.Contains(t, rec.Body.String(), "request_id is required")
	require.Zero(t, body.reads)
}
