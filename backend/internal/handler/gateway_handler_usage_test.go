package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageUnrestrictedIncludesWeeklyWindowStart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)

	weeklyWindowStart := time.Date(2026, time.July, 13, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	c.Set(string(middleware.ContextKeySubscription), &service.UserSubscription{
		WeeklyWindowStart: &weeklyWindowStart,
	})

	handler := &GatewayHandler{}
	handler.usageUnrestricted(
		c,
		context.Background(),
		&service.APIKey{Group: &service.Group{
			Name: "Weekly plan",
		}},
		middleware.AuthSubject{},
		nil,
		nil,
		nil,
		"USD",
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Subscription struct {
			WeeklyWindowStart *time.Time `json:"weekly_window_start"`
		} `json:"subscription"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.NotNil(t, response.Subscription.WeeklyWindowStart)
	require.True(t, weeklyWindowStart.Equal(*response.Subscription.WeeklyWindowStart))
}

func TestUsageUnrestrictedPreferredSubscriptionDoesNotExposeBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	preferredID := int64(99)
	c.Set(string(middleware.ContextKeyAPIKeyBilling), &middleware.APIKeyBillingContext{
		Mode: service.APIKeyBillingModeSubscription, Source: "subscription", Available: false,
	})

	(&GatewayHandler{}).usageUnrestricted(
		c,
		context.Background(),
		&service.APIKey{
			BillingMode:             service.APIKeyBillingModeSubscription,
			PreferredSubscriptionID: &preferredID,
			User:                    &service.User{Balance: 123},
		},
		middleware.AuthSubject{},
		nil,
		nil,
		nil,
		"USD",
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	billing, ok := response["billing"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "subscription", billing["source"])
	require.Equal(t, false, billing["available"])
	require.Equal(t, float64(preferredID), billing["preferred_subscription_id"])
	_, hasBalance := response["balance"]
	require.False(t, hasBalance)
}

func TestUsageUnrestrictedBalanceModeDoesNotExposeSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	c.Set(string(middleware.ContextKeyAPIKeyBilling), &middleware.APIKeyBillingContext{
		Mode: service.APIKeyBillingModeBalance, Source: "balance", Available: true,
	})

	(&GatewayHandler{}).usageUnrestricted(
		c,
		context.Background(),
		&service.APIKey{BillingMode: service.APIKeyBillingModeBalance, User: &service.User{Balance: 12.5}},
		middleware.AuthSubject{},
		nil,
		nil,
		nil,
		"USD",
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	billing, ok := response["billing"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "balance", billing["source"])
	require.Equal(t, float64(12.5), response["balance"])
	_, hasSubscription := response["subscription"]
	require.False(t, hasSubscription)
}
