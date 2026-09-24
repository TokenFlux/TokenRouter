//go:build unit

package httpapi

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/stretchr/testify/require"
)

func TestBuildGrokResponsesRequestPinsOAuthBaseURLAndUsesBearerToken(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"base_url": "https://xai.test/v1/",
		}},
	}

	req, err := (&GrokExecutor{Routes: gatewayprovider.GrokRoutes{Validate: xai.ValidateBaseURL}}).BuildResponsesRequest(context.Background(), nil, account, []byte(`{"model":"grok-4.3"}`), "access-token", "isolated-cache-id", false)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, "https://xai.test/v1/responses", req.URL.String())
	require.Equal(t, "Bearer access-token", req.Header.Get("Authorization"))
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Contains(t, req.Header.Get("Accept"), "text/event-stream")
	require.Equal(t, xai.CLIClientVersion, req.Header.Get("X-Grok-Client-Version"))
	require.Equal(t, "isolated-cache-id", req.Header.Get(GrokConversationIDHeader))

	data, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, `{"model":"grok-4.3"}`, strings.TrimSpace(string(data)))
}
func TestBuildGrokResponsesRequestAllowsPublicAPIKeyBaseURLByDefault(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
		Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://grok.example.test/v1/",
		}},
	}

	req, err := (&GrokExecutor{Routes: gatewayprovider.GrokRoutes{Validate: xai.ValidateBaseURL}}).BuildResponsesRequest(context.Background(), nil, account, []byte(`{"model":"grok-4.3"}`), "api-key", "", false)
	require.NoError(t, err)
	require.Equal(t, "https://grok.example.test/v1/responses", req.URL.String())
	require.Equal(t, "Bearer api-key", req.Header.Get("Authorization"))
	require.Empty(t, req.Header.Get("X-Grok-Client-Version"))
	require.NotEqual(t, xai.DefaultGrokUpstreamUserAgent(), req.Header.Get("User-Agent"))
}
func TestBuildGrokResponsesRequestHonorsOAuthOfficialEndpointSwitch(t *testing.T) {
	t.Parallel()

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"base_url": xai.DefaultBaseURL,
		}},
	}

	req, err := (&GrokExecutor{Routes: gatewayprovider.GrokRoutes{Validate: xai.ValidateBaseURL}}).BuildResponsesRequest(context.Background(), nil, account, []byte(`{"model":"grok-4.3"}`), "access-token", "", false)
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/responses", req.URL.String())
}
func TestBuildGrokResponsesRequestAppliesHeaderOverridesLast(t *testing.T) {
	t.Parallel()

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"base_url":                "https://relay.example.test/v1",
			"header_override_enabled": true,
			"header_overrides": map[string]any{
				"User-Agent":            "relay-client/2.0",
				"X-Grok-Client-Version": "9.9.9",
				"X-Relay-Token":         "relay-secret",
			},
		}},
	}

	req, err := (&GrokExecutor{Routes: gatewayprovider.GrokRoutes{Validate: xai.ValidateBaseURL}}).BuildResponsesRequest(context.Background(), nil, account, []byte(`{"model":"grok-4.3"}`), "access-token", "conv-1", false)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example.test/v1/responses", req.URL.String())
	// 覆写值优先于内置 CLI 身份头。名字不在 wire casing 映射中的覆写头
	// 以小写键直写（HTTP/2 线上语义），需按写入形态断言。
	require.Equal(t, "relay-client/2.0", req.Header.Get("User-Agent"))
	require.Equal(t, []string{"9.9.9"}, map[string][]string(req.Header)["x-grok-client-version"])
	require.Empty(t, req.Header.Get("X-Grok-Client-Version"))
	require.Equal(t, []string{"relay-secret"}, map[string][]string(req.Header)["x-relay-token"])
	// 会话路由头与认证头不受覆写影响。
	require.Equal(t, "conv-1", req.Header.Get(GrokConversationIDHeader))
	require.Equal(t, "Bearer access-token", req.Header.Get("Authorization"))
}
func TestBuildGrokResponsesRequestIgnoresBlockedHeaderOverrides(t *testing.T) {
	t.Parallel()

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
		Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"header_override_enabled": true,
			"header_overrides": map[string]any{
				"Authorization":  "Bearer stolen",
				"x-grok-conv-id": "pinned-conversation",
			},
		}},
	}

	req, err := (&GrokExecutor{Routes: gatewayprovider.GrokRoutes{Validate: xai.ValidateBaseURL}}).BuildResponsesRequest(context.Background(), nil, account, []byte(`{"model":"grok-4.3"}`), "api-key", "conv-2", false)
	require.NoError(t, err)
	require.Equal(t, "Bearer api-key", req.Header.Get("Authorization"))
	require.Equal(t, "conv-2", req.Header.Get(GrokConversationIDHeader))
}
func TestExtractGrokMediaModelSupportsJSONAndMultipart(t *testing.T) {
	require.Equal(t, "grok-imagine", gatewayprovider.GrokMediaCodec().ExtractGrokMediaModel("application/json", []byte(`{"model":"grok-imagine"}`)))

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.WriteField("prompt", "draw a cat"))
	require.NoError(t, writer.WriteField("model", "grok-imagine-edit"))
	require.NoError(t, writer.Close())

	require.Equal(t, "grok-imagine-edit", gatewayprovider.GrokMediaCodec().ExtractGrokMediaModel(writer.FormDataContentType(), buf.Bytes()))
}
