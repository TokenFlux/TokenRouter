package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// httpRPCFixture records private request fields and supplies transport-level failures.
type httpRPCFixture struct {
	request *pluginpb.HTTPRequest // most recent translated RPC request
	err     error                 // configured private transport failure
}

// Handle captures the translated request before returning a minimal valid plugin response.
func (f *httpRPCFixture) Handle(_ context.Context, request *pluginpb.HTTPRequest, _ ...grpc.CallOption) (*pluginpb.HTTPResponse, error) {
	f.request = request
	return &pluginpb.HTTPResponse{Status: 204, Headers: map[string]string{"Content-Type": "text/plain"}}, f.err
}

// TestHTTPTransportPreservesPrivateCallerAndMapsFailures sends a filtered request
// with a private grant and a nondefault language, then injects transport failures.
// The process receives the intended caller, locale, and address, while the
// browser-facing error contains only a stable reason and no private RPC details.
func TestHTTPTransportPreservesPrivateCallerAndMapsFailures(t *testing.T) {
	fixture := &httpRPCFixture{}
	client := &httpClient{client: fixture}
	input := &plugin.HTTPRequest{
		Method:   "POST",
		Path:     "/action",
		RawQuery: "a=b",
		ClientIP: "192.0.2.9",
		Caller: &plugin.HTTPCaller{
			UserID:   7,
			Username: "Reader",
			Role:     "user",
			Locale:   "zh-Hans",
			Grant:    "private-grant",
		},
	}
	response, err := client.Exchange(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, 204, response.Status)
	require.Equal(t, "private-grant", fixture.request.Caller.Grant)
	require.Equal(t, "zh-Hans", fixture.request.Caller.Locale)
	require.Equal(t, "192.0.2.9", fixture.request.ClientIp)
	fixture.err = status.Error(codes.DeadlineExceeded, "private-deadline-diagnostic")
	_, err = client.Exchange(t.Context(), input)
	require.ErrorIs(t, err, plugin.ErrPluginTimeout)
	fixture.err = status.Error(codes.Unavailable, "private-connection-diagnostic")
	_, err = client.Exchange(t.Context(), input)
	require.ErrorIs(t, err, plugin.ErrPluginRequestFailed)
	require.NotContains(t, err.Error(), "private-")
}
