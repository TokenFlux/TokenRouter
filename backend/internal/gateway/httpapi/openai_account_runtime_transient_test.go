package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type transientCooldownAccountRepo struct {
	gatewayprovider.ExecutionAccountStore
}

func (transientCooldownAccountRepo) SetOverloaded(context.Context, int64, time.Time) error {
	return nil
}

func TestHandleOpenAITransientError_BlocksOnlyRequestedModel(t *testing.T) {
	svc := newWSFixture(wsFixtureInputs{})
	setWSFixtureHealth(svc, newUpstreamHealthForTest(transientCooldownAccountRepo{}, &wsFixtureOptions{}, nil, accountcore.HealthOptions{}, nil))

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5105,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey},
	}

	firstShouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusBadGateway, http.Header{}, []byte(`{"error":{"message":"Upstream request failed","type":"upstream_error"}}`), false, "gpt-5.5").StopScheduling
	secondShouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusBadGateway, http.Header{}, []byte(`{"error":{"message":"Upstream request failed","type":"upstream_error"}}`), false, "gpt-5.5").StopScheduling

	require.False(t, firstShouldDisable)
	require.False(t, secondShouldDisable)
	require.False(t, wsFixtureAccountBlocked(svc, account))
	require.True(t, wsFixtureModelBlocked(svc, account, "gpt-5.5"))
	require.False(t, wsFixtureModelBlocked(svc, account, "gpt-5.6-terra"))
}

func TestHandleOpenAITransientError_TransientStatusesUseModelScope(t *testing.T) {
	for _, statusCode := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 520, 521, 522, 523, 524} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			svc := newWSFixture(wsFixtureInputs{})
			setWSFixtureHealth(svc, newUpstreamHealthForTest(transientCooldownAccountRepo{}, &wsFixtureOptions{}, nil, accountcore.HealthOptions{}, nil))

			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: int64(5100 + statusCode),
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeAPIKey},
			}

			firstShouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, statusCode, http.Header{}, []byte(`{"error":{"message":"temporary upstream failure"}}`), false, "gpt-5.5").StopScheduling
			secondShouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, statusCode, http.Header{}, []byte(`{"error":{"message":"temporary upstream failure"}}`), false, "gpt-5.5").StopScheduling

			require.False(t, firstShouldDisable)
			require.False(t, secondShouldDisable)
			require.False(t, wsFixtureAccountBlocked(svc, account), "status %d must not block the whole account", statusCode)
			require.True(t, wsFixtureModelBlocked(svc, account, "gpt-5.5"), "status %d should block the failing model", statusCode)
		})
	}
}

func TestHandleOpenAITransientError_529RemainsOverloadOnly(t *testing.T) {
	require.False(t, gatewayprovider.IsTransientAccountFailure(529, []byte(`{"error":{"message":"overloaded"}}`)))
}

func TestHandleOpenAITransientError_CanonicalModelIsNotMappedTwice(t *testing.T) {
	svc := newWSFixture(wsFixtureInputs{})
	setWSFixtureHealth(svc, newUpstreamHealthForTest(transientCooldownAccountRepo{}, &wsFixtureOptions{}, nil, accountcore.HealthOptions{}, nil))

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5107,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"public-alias": "upstream-a",
				"upstream-a":   "upstream-b",
			},
		}},
	}
	canonicalModel := gatewayprovider.ExecutionModelPolicy(account).Mapped("public-alias")
	require.Equal(t, "upstream-a", canonicalModel)

	for range 2 {
		gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusBadGateway, http.Header{}, []byte(`{"error":{"message":"temporary upstream failure"}}`), false, canonicalModel)
	}

	require.True(t, wsFixtureModelBlocked(svc, account, "public-alias"))
	svc.choices.ReportOpenAIAccountScheduleResult(account, canonicalModel, true, nil)
	require.False(t, wsFixtureModelBlocked(svc, account, "public-alias"))
}

func TestHandleOpenAITransientError_DoesNotBlockParameter400(t *testing.T) {
	svc := newWSFixture(wsFixtureInputs{})
	setWSFixtureHealth(svc, newUpstreamHealthForTest(transientCooldownAccountRepo{}, &wsFixtureOptions{}, nil, accountcore.HealthOptions{}, nil))

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5103,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey},
	}

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusBadRequest, http.Header{}, []byte(`{"error":{"message":"Invalid type for input[0].arguments"}}`), false, "gpt-5.5").StopScheduling

	require.False(t, shouldDisable)
	require.False(t, wsFixtureAccountBlocked(svc, account))
	require.False(t, wsFixtureModelBlocked(svc, account, "gpt-5.5"))
}

func TestHandleOpenAITransientError_HardDisableStillBlocksWholeAccount(t *testing.T) {
	svc := newWSFixture(wsFixtureInputs{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5106, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	wsFixtureBlockAccount(svc, account, time.Now().Add(time.Minute), "upstream_disable")

	require.True(t, wsFixtureRequestBlocked(svc, account, "gpt-5.5"))
	require.True(t, wsFixtureRequestBlocked(svc, account, "gpt-5.6-sol"))
}
