package httpapi

import (
	"context"
	"time"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// auxiliaryFixtureInputs 只组合辅助请求所需的原生依赖，不构造选择器或完成队列。
type auxiliaryFixtureInputs struct {
	allowHTTP     bool
	transport     httpclient.UpstreamTransport
	profiles      *egressprovider.TLSProfiles
	store         provider.ExecutionAccountStore
	credentials   *account.OpenAIExecutionCredentials
	observer      *accountprovider.UpstreamHealth
	authorization *account.OpenAIAuthorization
}

func newAuxiliaryFixture(v auxiliaryFixtureInputs) *OpenAIAuxiliary {
	blocks := account.NewRuntimeBlockState(time.Now)
	models := account.NewModelTransientState(0)
	credentials := v.credentials
	if credentials == nil {
		credentials = &account.OpenAIExecutionCredentials{}
	}
	if v.store != nil {
		credentials.Parent = func(ctx context.Context, id int64) (*account.Record, error) {
			a, err := v.store.GetByID(ctx, id)
			return provider.ExecutionRecord(a), err
		}
	}
	identity := provider.NewExecutionAgentIdentity(&account.OpenAITaskCoordinator{}, v.store, nil, nil)
	turns := &CodexTurnStateHeaders{Origins: session.NewCodexTurnOrigins(time.Now), TTL: func() time.Duration { return time.Hour }}
	requests := &OpenAIRequests{Options: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{AllowInsecureHTTP: v.allowHTTP}}, Accounts: v.store, Identity: identity, Credentials: credentials, Transport: v.transport, Profiles: v.profiles, Turns: turns, ClientPolicy: &accountprovider.OpenAIProbePolicy{Available: true, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Profiles: v.profiles}, Failure: &UpstreamTransportFailure{Health: &accountprovider.TransportHealth{Runtime: blocks}}}
	grok := &accountprovider.GrokHealth{Store: v.store, Health: v.observer, Runtime: blocks, ModelTransient: models, NormalizeModel: func(value *account.Record, model string) string {
		return (provider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
	}}
	output := &OpenAIResponseOutput{Options: OpenAIResponseOptions{Configured: true, ReadLimit: 128 * 1024 * 1024}, Health: &accountprovider.OpenAIResponseHealth{Health: v.observer, Runtime: blocks, ModelTransient: models}, GrokHealth: grok, Headers: egress.CompileHeaderFilter(egress.ResponseHeaderOptions{})}
	return &OpenAIAuxiliary{Requests: requests, Output: output, Authorization: v.authorization, CodexUsage: &accountprovider.CodexUsageObserver{Store: v.store, Throttle: account.NewWriteThrottle(30 * time.Second)}}
}
