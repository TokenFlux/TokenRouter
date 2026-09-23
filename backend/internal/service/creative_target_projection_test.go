//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestExecuteCreativeGrokEditUsesJSONEditEndpoint(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("edited-image"))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"data":[{"b64_json":%q}]}`, encoded)))},
		{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"data":[{"b64_json":%q}]}`, encoded)))},
	}}
	gateway := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 41,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "grok-test-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	run := creative.CreativeRun{Operation: creative.CreativeOperationEdit, RequestedOutputCount: 1, ImageSize: "2K", AspectRatio: "16:9"}
	payload := creative.CreativeRunPayload{Prompt: "edit this", Sources: []creative.CreativeInputImage{{Bytes: []byte("source"), Mime: "image/png"}}}
	outputs, err := gateway.CreativeTarget(account, nil, nil, nil).ExecuteGrok(context.Background(), run, payload, "grok-imagine-image-2.0")
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, []byte("edited-image"), outputs[0].Bytes)
	require.Equal(t, "https://xai.test/v1/images/edits", upstream.lastReq.URL.String())
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	var editBody map[string]any
	require.NoError(t, json.Unmarshal(upstream.lastBody, &editBody))
	require.Equal(t, "grok-imagine-image-2.0", editBody["model"])
	require.Equal(t, "b64_json", editBody["response_format"])
	require.Equal(t, "2k", editBody["resolution"])
	require.Equal(t, "16:9", editBody["aspect_ratio"])
	require.Equal(t, float64(1), editBody["n"])
	image, ok := editBody["image"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image_url", image["type"])
	require.Equal(t, "data:image/png;base64,c291cmNl", image["url"])

	generateRun := creative.CreativeRun{Operation: creative.CreativeOperationGenerate, RequestedOutputCount: 1, ImageSize: "1K"}
	_, err = gateway.CreativeTarget(account, nil, nil, nil).ExecuteGrok(context.Background(), generateRun, creative.CreativeRunPayload{Prompt: "generate"}, "grok-imagine-image-2.0")
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/images/generations", upstream.lastReq.URL.String())
}

// TestBuildCreativeOpenAIRequestBody 校验 OpenAI JSON/multipart 请求体。
func TestCreativeGeminiInpaintIsRejectedBeforeUpstream(t *testing.T) {
	upstream := &httpUpstreamRecorder{}
	gateway := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})
	_, err := gateway.CreativeTarget(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, nil, nil, nil).ExecuteGemini(context.Background(), creative.CreativeRun{Operation: creative.CreativeOperationInpaint}, creative.CreativeRunPayload{}, "gemini-3.1-flash-image")
	require.Error(t, err)
	require.False(t, creative.IsRetryableCreativeError(err))
	require.Empty(t, upstream.requests)
}
