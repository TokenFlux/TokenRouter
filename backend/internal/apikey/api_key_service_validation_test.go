//go:build unit

package apikey_test

import (
	"context"
	"math"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/stretchr/testify/require"
)

func TestValidateAPIKeyLimit(t *testing.T) {
	t.Parallel()

	for _, value := range []float64{0, 1, math.Nextafter(apiKeyLimitUpperBound, 0)} {
		require.NoError(t, apikey.ValidateAPIKeyLimit("quota", value))
	}

	for _, value := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), apiKeyLimitUpperBound, 1e100} {
		err := apikey.ValidateAPIKeyLimit("rate_limit_7d", value)
		require.ErrorIs(t, err, apikey.ErrAPIKeyLimitInvalid)
		require.Equal(t, "API_KEY_LIMIT_INVALID", apperror.Reason(err))
	}
}

func TestValidateCreateAPIKeyRequestNumericLimits(t *testing.T) {
	t.Parallel()

	positiveExpiry := 1
	require.NoError(t, apikey.KeyValidateCreateAPIKeyRequest(apikey.CreateAPIKeyRequest{
		Quota:         math.Nextafter(apiKeyLimitUpperBound, 0),
		RateLimit5h:   10,
		RateLimit1d:   20,
		RateLimit7d:   30,
		ExpiresInDays: &positiveExpiry,
	}))
	require.NoError(t, apikey.KeyValidateCreateAPIKeyRequest(apikey.CreateAPIKeyRequest{}))

	zeroExpiry, negativeExpiry := 0, -1
	invalid := []apikey.CreateAPIKeyRequest{
		{Quota: -1},
		{Quota: math.NaN()},
		{Quota: apiKeyLimitUpperBound},
		{RateLimit5h: math.Inf(1)},
		{RateLimit1d: -1},
		{RateLimit7d: 1e100},
		{ExpiresInDays: &zeroExpiry},
		{ExpiresInDays: &negativeExpiry},
	}
	for _, request := range invalid {
		require.Error(t, apikey.KeyValidateCreateAPIKeyRequest(request))
	}
}

func TestValidateUpdateAPIKeyRequestNumericLimits(t *testing.T) {
	t.Parallel()

	zero := 0.0
	largeValid := math.Nextafter(apiKeyLimitUpperBound, 0)
	require.NoError(t, apikey.KeyValidateUpdateAPIKeyRequest(apikey.UpdateAPIKeyRequest{
		Quota:       &zero,
		RateLimit7d: &largeValid,
		RateLimit1d: nil,
		RateLimit5h: nil,
	}))

	negative, nan, inf, tooLarge := -1.0, math.NaN(), math.Inf(1), float64(apiKeyLimitUpperBound)
	invalid := []apikey.UpdateAPIKeyRequest{
		{Quota: &negative},
		{RateLimit5h: &nan},
		{RateLimit1d: &inf},
		{RateLimit7d: &tooLarge},
	}
	for _, request := range invalid {
		require.ErrorIs(t, apikey.KeyValidateUpdateAPIKeyRequest(request), apikey.ErrAPIKeyLimitInvalid)
	}
}

func TestAPIKeyServiceRejectsInvalidLimitsBeforeRepositoryAccess(t *testing.T) {
	t.Parallel()

	service := newAPIKeyTestService(apiKeyTestDependencies{})
	// 故意不提供 context，确保数值校验先于任何仓储访问。
	var requestContext context.Context
	_, createErr := service.Create(requestContext, 1, apikey.CreateAPIKeyRequest{Quota: -1})
	require.ErrorIs(t, createErr, apikey.ErrAPIKeyLimitInvalid)

	invalid := math.Inf(1)
	_, updateErr := service.Update(requestContext, 1, 1, apikey.UpdateAPIKeyRequest{RateLimit5h: &invalid})
	require.ErrorIs(t, updateErr, apikey.ErrAPIKeyLimitInvalid)
}
