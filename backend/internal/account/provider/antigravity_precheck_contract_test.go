//go:build unit

package provider

import (
	"io"

	acct "github.com/TokenFlux/TokenRouter/internal/account"

	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/stretchr/testify/require"
)

func TestAntigravityRetryLoop_NoURLFallback_UsesConfiguredBaseURL(t *testing.T) {
	t.Setenv("GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL", "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	base1 := "https://ag-1.test"
	base2 := "https://ag-2.test"
	antigravity.BaseURLs = []string{base1, base2}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	upstream := &stubAntigravityUpstream{firstBase: base1, secondBase: base2}
	account := &acct.Record{
		ID:          1,
		Name:        "acc-1",
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
	}

	var handleErrorCalled bool
	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Prefix:      "[test]",
		Context:     context.Background(),
		Account:     account,
		ProxyURL:    "",
		AccessToken: "token",
		Action:      "generateContent",
		Body:        []byte(`{"input":"test"}`),
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", account.ID, account.Concurrency)
		},
		RequestedModel: "claude-sonnet-4-5",
		HandleError: func(int, http.Header, []byte) {
			handleErrorCalled = true
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Resp)
	defer func() { _ = result.Resp.Body.Close() }()
	require.Equal(t, http.StatusTooManyRequests, result.Resp.StatusCode)
	require.True(t, handleErrorCalled)
	require.Len(t, upstream.calls, antigravity.AntigravityMaxRetries)
	for _, callURL := range upstream.calls {
		require.True(t, strings.HasPrefix(callURL, base1))
	}

	available := antigravity.DefaultURLAvailability.GetAvailableURLs()
	require.NotEmpty(t, available)
	require.Equal(t, base1, available[0])
}

func TestAntigravityRetryLoop_PreCheck_SwitchesWhenRateLimited(t *testing.T) {
	upstream := &recordingOKUpstream{}
	account := &acct.Record{
		ID:          1,
		Name:        "acc-1",
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limit_reset_at": time.Now().Add(2 * time.Second).Format(time.RFC3339),
				},
			},
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:        context.Background(),
		Prefix:         "[test]",
		Account:        account,
		AccessToken:    "token",
		Action:         "generateContent",
		Body:           []byte(`{"input":"test"}`),
		RequestedModel: "claude-sonnet-4-5",
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", account.ID, account.Concurrency)
		},
		Sticky: true,
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.Nil(t, result)
	var switchErr *antigravity.AntigravityAccountSwitchError
	require.ErrorAs(t, err, &switchErr)
	require.Equal(t, account.ID, switchErr.OriginalAccountID)
	require.Equal(t, "claude-sonnet-4-5", switchErr.RateLimitedModel)
	require.True(t, switchErr.IsStickySession)
	require.Equal(t, 0, upstream.calls, "should not call upstream when switching on pre-check")
}

func TestAntigravityRetryLoop_PreCheck_SwitchesWhenRemainingLong(t *testing.T) {
	upstream := &recordingOKUpstream{}
	account := &acct.Record{
		ID:          2,
		Name:        "acc-2",
		Platform:    capability.PlatformAntigravity,
		Schedulable: true,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limit_reset_at": time.Now().Add(11 * time.Second).Format(time.RFC3339),
				},
			},
		},
	}

	svc := newAntigravityRetryFixture()
	adapter, input := svc.Bind(AntigravityRetryRequest{
		Context:        context.Background(),
		Prefix:         "[test]",
		Account:        account,
		AccessToken:    "token",
		Action:         "generateContent",
		Body:           []byte(`{"input":"test"}`),
		RequestedModel: "claude-sonnet-4-5",
		Do: func(req *http.Request) (*http.Response, error) {
			return upstream.Do(req, "", account.ID, account.Concurrency)
		},
		Sticky: true,
		HandleError: func(int, http.Header, []byte) {
		},
	})
	result, err := adapter.AntigravityRetryLoop(input)

	require.Nil(t, result)
	var switchErr *antigravity.AntigravityAccountSwitchError
	require.ErrorAs(t, err, &switchErr)
	require.Equal(t, account.ID, switchErr.OriginalAccountID)
	require.Equal(t, "claude-sonnet-4-5", switchErr.RateLimitedModel)
	require.True(t, switchErr.IsStickySession)
	require.Equal(t, 0, upstream.calls, "should not call upstream when switching on pre-check")
}

// 记录两个测试端点，证明原生循环不会按 URL 切换账号。
type stubAntigravityUpstream struct {
	firstBase, secondBase string
	calls                 []string
}

func (s *stubAntigravityUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	url := req.URL.String()
	s.calls = append(s.calls, url)
	if strings.HasPrefix(url, s.firstBase) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Resource has been exhausted"}}`))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}
