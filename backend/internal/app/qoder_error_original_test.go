package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQoderGatewayErrorDetailsAppliesPassthroughRule(t *testing.T) {
	customMessage := "Use another Qoder account"
	responseCode := http.StatusTeapot
	svc := errorpolicy.NewErrorPassthroughService(&qoderErrorPassthroughRepoStub{
		rules: []*errorpolicy.ErrorPassthroughRule{
			{
				Name:            "qoder custom",
				Enabled:         true,
				Priority:        1,
				ErrorCodes:      []int{http.StatusUnprocessableEntity},
				MatchMode:       errorpolicy.MatchModeAny,
				Platforms:       []string{capability.PlatformQoder},
				PassthroughCode: false,
				ResponseCode:    &responseCode,
				PassthroughBody: false,
				CustomMessage:   &customMessage,
				SkipMonitoring:  true,
			},
		},
	}, nil, gatewaytelemetry.ErrorRules,
	)
	svc.Start()
	t.Cleanup(svc.Stop)
	h := gatewayhttp.QoderErrorPresenter{Rules: svc, Describe: gatewayprovider.DescribeQoderError}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	status, errType, message, ok := h.Details(c, &qoder.APIError{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       `{"message":"original upstream message"}`,
		Message:    "original upstream message",
	})

	require.True(t, ok)
	require.Equal(t, responseCode, status)
	require.Equal(t, "upstream_error", errType)
	require.Equal(t, customMessage, message)
	skip, exists := c.Get(gatewayhttp.OpsSkipPassthroughKey)
	require.True(t, exists)
	require.Equal(t, true, skip)
}

type qoderErrorPassthroughRepoStub struct {
	rules []*errorpolicy.ErrorPassthroughRule
}

func (r *qoderErrorPassthroughRepoStub) List(context.Context) ([]*errorpolicy.ErrorPassthroughRule, error) {
	return r.rules, nil
}

func (r *qoderErrorPassthroughRepoStub) GetByID(context.Context, int64) (*errorpolicy.ErrorPassthroughRule, error) {
	return nil, nil
}

func (r *qoderErrorPassthroughRepoStub) Create(context.Context, *errorpolicy.ErrorPassthroughRule) (*errorpolicy.ErrorPassthroughRule, error) {
	return nil, nil
}

func (r *qoderErrorPassthroughRepoStub) Update(context.Context, *errorpolicy.ErrorPassthroughRule) (*errorpolicy.ErrorPassthroughRule, error) {
	return nil, nil
}

func (r *qoderErrorPassthroughRepoStub) Delete(context.Context, int64) error {
	return nil
}
