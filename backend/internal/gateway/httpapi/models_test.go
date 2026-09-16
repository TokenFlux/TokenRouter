package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 未消费请求不应读取正文，也不拥有取得槽位或资金检查的端口。
type modelForbiddenBody struct{}

func (modelForbiddenBody) Read([]byte) (int, error) { panic("models endpoint read request body") }
func (modelForbiddenBody) Close() error             { return nil }

type modelsBackendStub struct {
	ModelsBackend
	key          *apikey.APIKey
	result       routing.RequestableModelsResult
	response     *ModelHTTPResponse
	selectErr    error
	antigravity  bool
	paths        []string
	observations int
}

func (p *modelsBackendStub) Access(*gin.Context) (*apikey.APIKey, bool) { return p.key, p.key != nil }
func (p *modelsBackendStub) ForcedPlatform(*gin.Context) (string, bool) { return "", false }
func (p *modelsBackendStub) Available() bool                            { return true }
func (p *modelsBackendStub) Resolve(context.Context, *int64, string) routing.RequestableModelsResult {
	return p.result
}
func (p *modelsBackendStub) SelectGemini(context.Context, *int64) (GeminiModelReader, error) {
	if p.selectErr != nil {
		return nil, p.selectErr
	}
	return p, nil
}
func (p *modelsBackendStub) Read(_ context.Context, path string) (*ModelHTTPResponse, error) {
	p.paths = append(p.paths, path)
	return p.response, nil
}
func (p *modelsBackendStub) HasAntigravity(context.Context, *int64) (bool, error) {
	return p.antigravity, nil
}
func (p *modelsBackendStub) CapacityLimited(*gin.Context, error) { p.observations++ }
func (p *modelsBackendStub) SafeModelSegment(m string) bool      { return m != "bad/model" }

// 未被约定回退分支调用的目录方法保持未实现，避免空成功恢复默认列表。
type modelsCatalogStub struct {
	ModelsCatalog
	fallbacks int
}

func (p *modelsCatalogStub) GeminiList(bool) GeminiModelsList {
	p.fallbacks++
	return GeminiModelsList{Models: []GeminiModel{{Name: "models/fallback"}}}
}
func (p *modelsCatalogStub) GeminiModel(name string, _ bool) GeminiModel {
	p.fallbacks++
	return GeminiModel{Name: "models/" + name}
}
func (p *modelsCatalogStub) HasGeminiFallback(name string) bool { return name == "known" }
func modelsContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Request.Body = modelForbiddenBody{}
	return c, w
}
func TestModelsBoundEmptyDoesNotFallBackOrConsume(t *testing.T) {
	p := &modelsBackendStub{key: prefaceKey()}
	c, w := modelsContext()
	NewModelsHandler(p, &modelsCatalogStub{}).Models(c)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"object":"list","data":[]}`, w.Body.String())
	require.Empty(t, p.paths)
}
func TestGeminiModelsSuccessfulEmptyPreservesResponse(t *testing.T) {
	p := &modelsBackendStub{key: prefaceKey(), response: &ModelHTTPResponse{StatusCode: 200, Headers: http.Header{"Content-Type": []string{"application/json"}, "X-Test": []string{"one", "two"}, "Connection": []string{"keep-alive"}, "Content-Length": []string{"999"}}, Body: []byte(`{"models":[],"nextPageToken":"next"}`)}}
	catalog := &modelsCatalogStub{}
	h := NewModelsHandler(p, catalog)
	c, w := modelsContext()
	h.GeminiV1BetaListModels(c)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"models":[],"nextPageToken":"next"}`, w.Body.String())
	require.Equal(t, []string{"/v1beta/models"}, p.paths)
	require.Equal(t, []string{"one", "two"}, w.Header().Values("X-Test"))
	require.Empty(t, w.Header().Get("Connection"))
	require.NotEqual(t, "999", w.Header().Get("Content-Length"))
	require.Zero(t, catalog.fallbacks)
}
func TestGeminiModelsScopeFallbackKeepsOtherFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		fallback bool
	}{
		{"scope", 401, `{"error":"ACCESS_TOKEN_SCOPE_INSUFFICIENT"}`, true},
		{"forbidden", 403, `{"error":"forbidden"}`, false},
		{"server", 503, `{"error":"unavailable"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &modelsBackendStub{key: prefaceKey(), response: &ModelHTTPResponse{StatusCode: tc.status, Headers: http.Header{}, Body: []byte(tc.body)}}
			catalog := &modelsCatalogStub{}
			c, w := modelsContext()
			NewModelsHandler(p, catalog).GeminiV1BetaListModels(c)
			if tc.fallback {
				require.Equal(t, 200, w.Code)
				require.Equal(t, 1, catalog.fallbacks)
			} else {
				require.Equal(t, tc.status, w.Code)
				require.JSONEq(t, tc.body, w.Body.String())
				require.Zero(t, catalog.fallbacks)
			}
		})
	}
}
func TestGeminiModelGetKnownFallbackAndPathValidation(t *testing.T) {
	for _, name := range []string{"known", "unknown", "bad/model"} {
		t.Run(name, func(t *testing.T) {
			p := &modelsBackendStub{key: prefaceKey(), response: &ModelHTTPResponse{StatusCode: 404, Headers: http.Header{}, Body: []byte(`{"error":"missing"}`)}}
			catalog := &modelsCatalogStub{}
			c, w := modelsContext()
			c.Params = gin.Params{{Key: "model", Value: "/" + name}}
			NewModelsHandler(p, catalog).GeminiV1BetaGetModel(c)
			switch name {
			case "known":
				require.Equal(t, 200, w.Code)
				require.Equal(t, 1, catalog.fallbacks)
			case "unknown":
				require.Equal(t, 404, w.Code)
				require.Zero(t, catalog.fallbacks)
			default:
				require.Equal(t, 400, w.Code)
				require.Empty(t, p.paths)
			}
		})
	}
}
func TestGeminiModelsSelectionFailureKeepsCapacityObservation(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		p := &modelsBackendStub{key: prefaceKey(), selectErr: errors.New("no account"), antigravity: fallback}
		catalog := &modelsCatalogStub{}
		c, w := modelsContext()
		NewModelsHandler(p, catalog).GeminiV1BetaListModels(c)
		require.Empty(t, p.paths)
		if fallback {
			require.Equal(t, 200, w.Code)
			require.Zero(t, p.observations)
		} else {
			require.Equal(t, 503, w.Code)
			require.Equal(t, 1, p.observations)
			require.Contains(t, w.Body.String(), "No available Gemini accounts: no account")
		}
	}
}
