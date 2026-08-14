//go:build unit

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTeamAPIKeyErrorsHaveStableGatewayStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "feature_disabled", err: service.ErrTeamFeatureDisabled, wantStatus: http.StatusForbidden, wantCode: "TEAM_FEATURE_DISABLED"},
		{name: "team_suspended", err: service.ErrTeamSuspended, wantStatus: http.StatusForbidden, wantCode: "TEAM_SUSPENDED"},
		{name: "membership_missing", err: service.ErrTeamMembershipRequired, wantStatus: http.StatusForbidden, wantCode: "TEAM_MEMBERSHIP_REQUIRED"},
		{name: "actor_inactive", err: service.ErrTeamActorInactive, wantStatus: http.StatusForbidden, wantCode: "TEAM_ACTOR_INACTIVE"},
		{name: "owner_inactive", err: service.ErrTeamBillingOwnerInactive, wantStatus: http.StatusForbidden, wantCode: "TEAM_BILLING_OWNER_INACTIVE"},
		{name: "daily_limit", err: service.ErrTeamMemberDailyExceeded, wantStatus: http.StatusTooManyRequests, wantCode: "TEAM_MEMBER_DAILY_LIMIT_EXCEEDED"},
		{name: "weekly_limit", err: service.ErrTeamMemberWeeklyExceeded, wantStatus: http.StatusTooManyRequests, wantCode: "TEAM_MEMBER_WEEKLY_LIMIT_EXCEEDED"},
		{name: "monthly_limit", err: service.ErrTeamMemberMonthlyExceeded, wantStatus: http.StatusTooManyRequests, wantCode: "TEAM_MEMBER_MONTHLY_LIMIT_EXCEEDED"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			require.True(t, abortTeamAPIKeyError(ctx, test.err))
			require.Equal(t, test.wantStatus, recorder.Code)
			require.Contains(t, recorder.Body.String(), test.wantCode)

			googleStatus, _, ok := googleTeamAPIKeyError(test.err)
			require.True(t, ok)
			require.Equal(t, test.wantStatus, googleStatus)
		})
	}
}

func TestTeamMemberLimitsSkipNonConsumingRequests(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/usage"},
		{method: http.MethodGet, path: "/v1/models"},
		{method: http.MethodGet, path: "/v1/images/batches"},
		{method: http.MethodGet, path: "/v1/images/batches/test"},
		{method: http.MethodDelete, path: "/v1/images/batches/test"},
		{method: http.MethodDelete, path: "/v1/images/batches/test/outputs"},
		{method: http.MethodPost, path: "/v1/images/batches/test/cancel"},
		{method: http.MethodPost, path: "/v1/messages/count_tokens"},
	}
	for _, test := range tests {
		require.True(t, isAPIKeyNonConsumingRequest(test.method, test.path), "%s %s", test.method, test.path)
	}
	require.False(t, isAPIKeyNonConsumingRequest(http.MethodGet, "/v1/sub2api/billing"))
	require.False(t, isAPIKeyNonConsumingRequest(http.MethodPost, "/v1/messages"))
	require.False(t, isAPIKeyNonConsumingRequest(http.MethodPost, "/v1/images/batches"))
}
