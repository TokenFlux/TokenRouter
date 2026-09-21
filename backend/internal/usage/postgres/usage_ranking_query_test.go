package postgres

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestUsageRankingQueryPartsFollowSelectedMetric(t *testing.T) {
	tests := []struct {
		name                 string
		sortBy               usage.UsageRankingSortBy
		rawEligibility       string
		analyticsEligibility string
		orderBy              string
	}{
		{
			name:                 "total tokens",
			sortBy:               usage.UsageRankingSortByTotalTokens,
			rawEligibility:       "SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens)",
			analyticsEligibility: "SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens)",
			orderBy:              "total_tokens DESC, requests DESC, actual_cost DESC, user_id ASC",
		},
		{
			name:                 "requests",
			sortBy:               usage.UsageRankingSortByRequests,
			rawEligibility:       "COUNT(*) > 0",
			analyticsEligibility: "SUM(total_requests)",
			orderBy:              "requests DESC, total_tokens DESC, actual_cost DESC, user_id ASC",
		},
		{
			name:                 "actual cost",
			sortBy:               usage.UsageRankingSortByActualCost,
			rawEligibility:       "SUM(u.actual_cost)",
			analyticsEligibility: "SUM(actual_cost)",
			orderBy:              "actual_cost DESC, total_tokens DESC, requests DESC, user_id ASC",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rawEligibility, orderBy := usageRankingQueryParts(test.sortBy)
			require.Contains(t, rawEligibility, test.rawEligibility)
			require.Equal(t, test.orderBy, orderBy)
			require.Contains(t, usageRankingAnalyticsEligibility(test.sortBy), test.analyticsEligibility)
		})
	}
}
