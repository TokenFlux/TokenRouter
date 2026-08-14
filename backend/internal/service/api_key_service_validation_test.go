//go:build unit

package service

import (
	"math"
	"testing"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestValidateAPIKeyLimit(t *testing.T) {
	t.Parallel()

	for _, value := range []float64{0, 1, math.Nextafter(apiKeyLimitUpperBound, 0)} {
		require.NoError(t, ValidateAPIKeyLimit("quota", value))
	}

	for _, value := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), apiKeyLimitUpperBound, 1e100} {
		err := ValidateAPIKeyLimit("rate_limit_7d", value)
		require.ErrorIs(t, err, ErrAPIKeyLimitInvalid)
		require.Equal(t, "API_KEY_LIMIT_INVALID", infraerrors.Reason(err))
	}
}

func TestValidateCreateAPIKeyRequestNumericLimits(t *testing.T) {
	t.Parallel()

	positiveExpiry := 1
	require.NoError(t, validateCreateAPIKeyRequest(CreateAPIKeyRequest{
		Quota:         math.Nextafter(apiKeyLimitUpperBound, 0),
		RateLimit5h:   10,
		RateLimit1d:   20,
		RateLimit7d:   30,
		ExpiresInDays: &positiveExpiry,
	}))
	require.NoError(t, validateCreateAPIKeyRequest(CreateAPIKeyRequest{}))

	zeroExpiry, negativeExpiry := 0, -1
	invalid := []CreateAPIKeyRequest{
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
		require.Error(t, validateCreateAPIKeyRequest(request))
	}
}

func TestValidateUpdateAPIKeyRequestNumericLimits(t *testing.T) {
	t.Parallel()

	zero := 0.0
	largeValid := math.Nextafter(apiKeyLimitUpperBound, 0)
	require.NoError(t, validateUpdateAPIKeyRequest(UpdateAPIKeyRequest{
		Quota:       &zero,
		RateLimit7d: &largeValid,
		RateLimit1d: nil,
		RateLimit5h: nil,
	}))

	negative, nan, inf, tooLarge := -1.0, math.NaN(), math.Inf(1), float64(apiKeyLimitUpperBound)
	invalid := []UpdateAPIKeyRequest{
		{Quota: &negative},
		{RateLimit5h: &nan},
		{RateLimit1d: &inf},
		{RateLimit7d: &tooLarge},
	}
	for _, request := range invalid {
		require.ErrorIs(t, validateUpdateAPIKeyRequest(request), ErrAPIKeyLimitInvalid)
	}
}

func TestAPIKeyServiceRejectsInvalidLimitsBeforeRepositoryAccess(t *testing.T) {
	t.Parallel()

	service := &APIKeyService{}
	_, createErr := service.Create(nil, 1, CreateAPIKeyRequest{Quota: -1})
	require.ErrorIs(t, createErr, ErrAPIKeyLimitInvalid)

	invalid := math.Inf(1)
	_, updateErr := service.Update(nil, 1, 1, UpdateAPIKeyRequest{RateLimit5h: &invalid})
	require.ErrorIs(t, updateErr, ErrAPIKeyLimitInvalid)
}
