//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 读取器在可见输出和用量之后报错，用于核对新旧返回入口的契约。
type grokObservationErrorReader struct{ err error }

func (r grokObservationErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestGrokNativeObservationRetainsPartialResultWithoutChangingLegacyFailure(t *testing.T) {
	failure := errors.New("fixture truncated stream")
	payload := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\",\"usage\":{\"input_tokens\":9,\"output_tokens\":2}}\n\n"
	for _, native := range []bool{true, false} {
		name := "legacy"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			account := &Account{ID: 470, Platform: PlatformGrok, Type: AccountTypeAPIKey}
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader(payload), grokObservationErrorReader{failure}))}
			service := &OpenAIGatewayService{}
			var result *openaiStreamingResult
			var err error
			if native {
				result, err = service.readStreamingResponseObservation(context.Background(), response, c, account, time.Now(), "grok-fixture", "grok-fixture", "")
			} else {
				result, err = service.handleStreamingResponse(context.Background(), response, c, account, time.Now(), "grok-fixture", "grok-fixture")
			}
			require.Error(t, err)
			require.Contains(t, recorder.Body.String(), "visible")
			if native {
				require.NotNil(t, result)
				require.True(t, result.served)
				require.True(t, result.hasUsage)
				require.True(t, result.httpCommitted)
				require.NotNil(t, result.firstSemanticOutput)
				require.Equal(t, 9, result.usage.InputTokens)
				require.Equal(t, 2, result.usage.OutputTokens)
			}
			// 此路径的旧读取器原本返回错误和用量；不因新入口改变两者。
			if !native {
				require.NotNil(t, result)
				require.False(t, result.observedOnly)
				require.Equal(t, 9, result.usage.InputTokens)
			}
		})
	}
}
