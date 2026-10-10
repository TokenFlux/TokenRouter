package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/provider"
)

// decisionTestLoader 保存 HTTP 表单传给测试服务的字段。
type decisionTestLoader struct {
	request provider.TestRequest
	loads   int
}

func (l *decisionTestLoader) LoadTestTarget(_ context.Context, request provider.TestRequest) (provider.TestTarget, error) {
	l.loads++
	l.request = request
	return l, nil
}

func (l *decisionTestLoader) Information() provider.TestTargetInfo {
	return provider.TestTargetInfo{ProviderSnapshot: provider.ProviderSnapshot{Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey}}
}

func (l *decisionTestLoader) Execute(ctx context.Context, _ provider.PreparedTestRequest, sink provider.TestEventSink) error {
	if err := sink.Begin(ctx, true); err != nil {
		return err
	}
	return sink.Emit(ctx, provider.TestEvent{Type: "test_complete", Success: true})
}

// TestTestHandlerDecisionJSON 检查结构化表单绑定和损坏 JSON 的早期拒绝。
func TestTestHandlerDecisionJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		loads      int
	}{
		{"decision", `{"model_id":"jev-latest","test_type":"decision","systemone":{"state":["ready"],"questions":{"x":{"type":"noul","instructions":"Ready?"}}}}`, http.StatusOK, 1},
		{"empty probe", ``, http.StatusOK, 1},
		{"invalid", `{"systemone":`, http.StatusBadRequest, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader := &decisionTestLoader{}
			router := gin.New()
			router.POST("/providers/:id/test", NewTestHandler(provider.NewTestService(loader, provider.TestOptions{}), nil).Test)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/providers/42/test", strings.NewReader(tc.body)))
			require.Equal(t, tc.status, rec.Code)
			require.Equal(t, tc.loads, loader.loads)
			if tc.name == "decision" {
				require.Equal(t, int64(42), loader.request.ProviderID)
				require.Equal(t, "decision", *loader.request.Type)
				require.JSONEq(t, `{"state":["ready"],"questions":{"x":{"type":"noul","instructions":"Ready?"}}}`, string(loader.request.SystemOne))
			}
		})
	}
}
