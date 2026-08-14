//go:build unit

package handler

import (
	"math"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestValidateAPIKeyCreateRequest(t *testing.T) {
	t.Parallel()

	zero := 0.0
	largeValid := math.Nextafter(1_000_000_000_000, 0)
	positiveDays := 1
	require.NoError(t, validateAPIKeyCreateRequest(CreateAPIKeyRequest{
		Quota:         &zero,
		RateLimit5h:   &largeValid,
		ExpiresInDays: &positiveDays,
	}))
	require.NoError(t, validateAPIKeyCreateRequest(CreateAPIKeyRequest{}))

	negative, nan, inf, tooLarge := -1.0, math.NaN(), math.Inf(1), 1_000_000_000_000.0
	zeroDays, negativeDays := 0, -1
	invalid := []CreateAPIKeyRequest{
		{Quota: &negative},
		{Quota: &nan},
		{RateLimit5h: &inf},
		{RateLimit1d: &negative},
		{RateLimit7d: &tooLarge},
		{ExpiresInDays: &zeroDays},
		{ExpiresInDays: &negativeDays},
	}
	for _, request := range invalid {
		require.Error(t, validateAPIKeyCreateRequest(request))
	}
}

func TestValidateAPIKeyUpdateRequest(t *testing.T) {
	t.Parallel()

	zero := 0.0
	largeValid := math.Nextafter(1_000_000_000_000, 0)
	require.NoError(t, validateAPIKeyUpdateRequest(UpdateAPIKeyRequest{
		Quota:       &zero,
		RateLimit7d: &largeValid,
	}))

	negative, nan, inf, tooLarge := -1.0, math.NaN(), math.Inf(-1), 1e100
	invalid := []UpdateAPIKeyRequest{
		{Quota: &negative},
		{RateLimit5h: &nan},
		{RateLimit1d: &inf},
		{RateLimit7d: &tooLarge},
	}
	for _, request := range invalid {
		require.ErrorIs(t, validateAPIKeyUpdateRequest(request), service.ErrAPIKeyLimitInvalid)
	}
}
