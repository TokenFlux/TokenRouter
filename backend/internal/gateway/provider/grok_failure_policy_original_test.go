//go:build unit

package provider_test

import (
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestClassifyGrokUpstreamFailure_FreeUsage(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "code free-usage-exhausted",
			status: http.StatusTooManyRequests,
			body:   `{"error":{"code":"subscription:free-usage-exhausted","message":"You've used all the included free usage for model grok-4.5. Usage resets over a rolling 24-hour window."}}`,
		},
		{
			name:   "chinese body without 429",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"模型额度用完，请稍后再试"}}`,
		},
		{
			name:   "token pair with free marker",
			status: http.StatusOK,
			body:   `{"error":{"message":"free usage tokens (actual / limit): 2000000 / 2000000 for model grok-4.5"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := grok.ClassifyGrokUpstreamFailure(tc.status, []byte(tc.body), "grok-4.5")
			require.Equal(t, grok.GrokFailureFreeUsage, d.Class)
			require.True(t, d.ShouldCooldown)
			require.True(t, d.ShouldFailover)
			require.False(t, d.BlockModel, "free-usage must not soft-block models")
			require.Equal(t, grok.GrokFreeUsageProbeCooldown, d.Cooldown)
		})
	}
}

func TestClassifyGrokUpstreamFailure_EmptyUpstream(t *testing.T) {
	d := grok.ClassifyGrokUpstreamFailure(http.StatusBadGateway, []byte(`empty model output: no content/tool_calls`), "grok-4.5")
	require.Equal(t, grok.GrokFailureEmptyUpstream, d.Class)
	require.True(t, d.ShouldCooldown)
	require.True(t, d.ShouldFailover)
	require.True(t, d.BlockModel)
	require.Equal(t, 4*time.Minute, d.Cooldown)
}

func TestClassifyGrokUpstreamFailure_ModelCapacityUsesShortCooldown(t *testing.T) {
	d := grok.ClassifyGrokUpstreamFailure(http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"The model is currently at capacity due to high demand"}}`), "grok-4.6")
	require.Equal(t, grok.GrokFailureModelCapacity, d.Class)
	require.Equal(t, time.Minute, d.Cooldown)
	require.False(t, d.BlockModel)
}

func TestClassifyGrokUpstreamFailure_Billing(t *testing.T) {
	d := grok.ClassifyGrokUpstreamFailure(http.StatusForbidden, []byte(`{"code":"personal-team-blocked:spending-limit","error":"spending limit reached"}`), "")
	require.Equal(t, grok.GrokFailureBilling, d.Class)
	require.True(t, d.ShouldCooldown)
	require.True(t, d.ShouldFailover)
}

func TestClassifyGrokUpstreamFailure_GrokSubscriptionRequiredIsBilling(t *testing.T) {
	d := grok.ClassifyGrokUpstreamFailure(http.StatusPaymentRequired,
		[]byte(`{"error":{"message":"You have run out of credits or need a Grok subscription"}}`), "grok-4.6")
	require.Equal(t, grok.GrokFailureBilling, d.Class)
	require.True(t, d.ShouldFailover)
	require.True(t, d.ShouldCooldown)
}

func TestGrokRetryableOnSameAccount_CapacityAndRateLimit(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9105, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	require.True(t, gatewayprovider.GrokRetryableOnSameAccount(account, http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"The model is currently at capacity due to high demand"}}`)))
	require.False(t, gatewayprovider.GrokRetryableOnSameAccount(account, http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"rate limit exceeded"}}`)))
	require.False(t, gatewayprovider.GrokRetryableOnSameAccount(account, http.StatusPaymentRequired,
		[]byte(`{"error":{"message":"You have run out of credits or need a Grok subscription"}}`)))
	poolAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9108, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"pool_mode": true}}}
	require.False(t, gatewayprovider.GrokRetryableOnSameAccount(poolAccount, http.StatusTooManyRequests,
		[]byte(`{"error":{"code":"subscription:free-usage-exhausted"}}`)),
		"pool free-usage must fail over instead of retrying the exhausted account")
	require.False(t, gatewayprovider.GrokRetryableOnSameAccount(account, http.StatusBadRequest,
		[]byte(`{"error":{"message":"capacity field is invalid"}}`)))
	nonGrok := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9106, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	require.False(t, gatewayprovider.GrokRetryableOnSameAccount(nonGrok, http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"model at capacity"}}`)))
}

func TestShouldMarkGrokTeamModelRateLimit_ExcludesCapacity(t *testing.T) {
	require.False(t, grok.ShouldMarkGrokTeamModelRateLimit(http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"The model is currently at capacity due to high demand"}}`)))
	require.True(t, grok.ShouldMarkGrokTeamModelRateLimit(http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"rate limit exceeded"}}`)))
	require.True(t, grok.ShouldMarkGrokTeamModelRateLimit(http.StatusBadRequest,
		[]byte(`{"error":{"code":"subscription:free-usage-exhausted"}}`)))
	require.False(t, grok.ShouldMarkGrokTeamModelRateLimit(http.StatusBadRequest,
		[]byte(`{"error":{"message":"invalid request"}}`)))
}

func TestGrokSameAccountRetryMetadata_CapacityDeadline(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9107, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	retryable, delay, deadline, retryMax := gatewayprovider.GrokSameAccountRetryMetadata(account, http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"model capacity exceeded"}}`))
	require.True(t, retryable)
	require.Equal(t, 500*time.Millisecond, delay)
	require.WithinDuration(t, time.Now().Add(30*time.Second), deadline, 2*time.Second)
	require.Equal(t, 1, retryMax)

	retryable, delay, deadline, retryMax = gatewayprovider.GrokSameAccountRetryMetadata(account, http.StatusTooManyRequests,
		[]byte(`{"error":{"message":"rate limit exceeded"}}`))
	require.False(t, retryable)
	require.Zero(t, delay)
	require.True(t, deadline.IsZero())
	require.Zero(t, retryMax)
}

func TestClassifyGrokUpstreamFailure_ValidationNoCool(t *testing.T) {
	d := grok.ClassifyGrokUpstreamFailure(http.StatusBadRequest, []byte(`{"error":{"message":"invalid tool schema"}}`), "")
	require.Equal(t, grok.GrokFailureNone, d.Class)
	require.False(t, d.ShouldCooldown)
	require.False(t, d.ShouldFailover)
}

func TestClassifyGrokUpstreamFailure_FreeUsageWinsOver5xx(t *testing.T) {
	// 代理可能把免费额度错误改写为合成的 502，此时应以响应体为准。
	d := grok.ClassifyGrokUpstreamFailure(http.StatusBadGateway, []byte(`subscription:free-usage-exhausted for model grok-4.3`), "grok-4.3")
	require.Equal(t, grok.GrokFailureFreeUsage, d.Class)
	require.NotEqual(t, grok.GrokFailureServer, d.Class)
}

func TestClassifyGrokUpstreamFailure_CompatibilityDoesNotCooldown(t *testing.T) {
	cases := []string{
		`{"error":{"message":"Could not decode the compaction blob. Ensure it is unmodified from the compact response"}}`,
		`{"code":"compaction_decode_error","message":"invalid response history"}`,
	}
	for _, body := range cases {
		d := grok.ClassifyGrokUpstreamFailure(http.StatusUnprocessableEntity, []byte(body), "grok-4.6")
		require.Equal(t, grok.GrokFailureCompatibility, d.Class, body)
		require.True(t, d.ShouldFailover, body)
		require.False(t, d.ShouldCooldown, body)
		require.Zero(t, d.Cooldown, body)
	}
}

func TestClassifyGrokUpstreamFailure_CompatibilityRequiresClientError(t *testing.T) {
	body := []byte(`{"error":{"message":"upstream failed while handling the compaction blob"}}`)
	for _, status := range []int{http.StatusBadGateway, http.StatusInternalServerError} {
		d := grok.ClassifyGrokUpstreamFailure(status, body, "grok-4.6")
		require.NotEqual(t, grok.GrokFailureCompatibility, d.Class)
		require.True(t, d.ShouldCooldown)
	}
}

func TestClassifyGrokUpstreamFailure_GenericShapeErrorDoesNotFailover(t *testing.T) {
	d := grok.ClassifyGrokUpstreamFailure(http.StatusBadRequest,
		[]byte(`{"error":{"message":"data did not match any variant of the untagged enum content"}}`), "grok-4.6")
	require.NotEqual(t, grok.GrokFailureCompatibility, d.Class)
	require.False(t, d.ShouldFailover)
}

func TestShouldFailoverGrokUpstreamError_FreeUsageBody(t *testing.T) {

	body := []byte(`{"error":{"code":"subscription:free-usage-exhausted","message":"free usage exhausted"}}`)
	require.True(t, gatewayprovider.ShouldFailoverGrokResponse(http.StatusBadRequest, body))
}

func TestShouldFailoverGrokUpstreamError_CompatibilityBody(t *testing.T) {

	body := []byte(`{"error":{"message":"Could not decode the compaction blob"}}`)
	require.True(t, gatewayprovider.ShouldFailoverGrokResponse(http.StatusUnprocessableEntity, body))
}

func TestShouldFailoverGrokUpstreamError_ContentPolicyStillNoFailover(t *testing.T) {

	body := []byte(`{"error":{"code":"new_sensitive","message":"text is sensitive"}}`)
	require.False(t, gatewayprovider.ShouldFailoverGrokResponse(http.StatusForbidden, body))
}
