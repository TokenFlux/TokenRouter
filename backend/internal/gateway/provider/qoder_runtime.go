package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"

	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoder_runtime.go constructs Qoder execution targets and publishes the
// runtime executor with its activity callback.
// Qoder requests use a conversation store and a lazily constructed executor
// shared by this runtime. The activity callback is captured on the first
// executor read, so application binding precedes request execution. Provider
// mapping rewrites the wire model while the caller supplies its response
// identity separately.
//
// Targets defer credential, attribute, transport, and site-profile lookup to
// callbacks using the provider record. A bad site can appear as Global in
// initial metadata but still fail production client creation, while an
// injected nonproduction client can bypass that creation path. The mutex
// coordinates executor publication and callback binding inside this process;
// callers serialize access to borrowed provider values and own selection,
// funding, and retries.

// QoderRuntimeOptions binds app-owned token, transport, attribute, health, and
// conversation dependencies.
type QoderRuntimeOptions struct {
	// request model attribute service
	Attributes *routing.ModelAttributeService
	// Qoder session credential source
	Tokens *provideradapter.QoderTokenProvider
	// Client is reused when it is not a concrete production qoder.Client.
	// A concrete production client is rebuilt from the provider site profile.
	Client qoder.StreamClient
	// shared Qoder request transport
	Transport provideradapter.QoderTransport
	// provider TLS profile reader
	Profiles *egressprovider.TLSProfiles
	// provider failure state writer
	Health provideradapter.QoderHealthStore
	// Conversations can supply the app-owned conversation store. Nil selects a
	// runtime-owned store with QoderConversationTTL when NewQoderRuntime constructs it.
	Conversations *qoder.QoderConversationStore
}

// QoderRuntime lazily owns a shared executor using its bound conversation
// store.
type QoderRuntime struct {
	options QoderRuntimeOptions // bound app execution dependencies
	// mu protects executor publication and enter binding. The runtime must stay
	// at one address once those methods start using the lock.
	mu sync.Mutex
	// shared executor conversation storage
	conversations *qoder.QoderConversationStore
	// executor is created once under mu using the current enter callback.
	// It remains the same object for subsequent attempts handled by this runtime.
	executor *qoder.Executor
	// enter is captured when executor is created; bind it before that first
	// read so application activity admission covers each platform attempt.
	enter func() (func(), error)
}

// NewQoderRuntime binds dependencies and uses the supplied conversation store
// or creates one with QoderConversationTTL. Construction starts no execution;
// provider selection, funding, and retry policy remain with the gateway
// caller.
func NewQoderRuntime(options QoderRuntimeOptions) *QoderRuntime {
	conversations := options.Conversations
	if conversations == nil {
		conversations = qoder.NewQoderConversationStore(qoder.QoderConversationTTL)
	}
	return &QoderRuntime{options: options, conversations: conversations}
}

// BindAttemptActivity sets the activity callback under the runtime mutex. Bind
// it before the first Executor call, since an already constructed executor
// keeps the callback captured when it was created.
func (r *QoderRuntime) BindAttemptActivity(enter func() (func(), error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enter = enter
}

// Executor lazily constructs and returns one Qoder executor under the runtime
// mutex. It captures the conversation store and current activity callback at
// first construction, so later bindings do not modify that executor.
func (r *QoderRuntime) Executor() *qoder.Executor {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Capture activity and conversation dependencies exactly when publishing this
	// executor so every subsequent caller receives the same initialized execution owner.
	if r.executor == nil {
		r.executor = qoder.NewExecutor(qoder.ExecuteOptions{Conversations: r.conversations, Enter: r.enter})
	}
	return r.executor
}

// PrepareQoderTarget returns the shared executor and one attempt input with
// mapped model, stream flag, and platform target. Its responseModel remains
// the caller identity used by output, while the wire body applies provider
// mapping.
func (r *QoderRuntime) PrepareQoderTarget(metadata qoder.RequestMetadata, value *provider.Record, body []byte, wire protocol.ProtocolID, responseModel string) (upstream.Executor, upstream.AttemptInput) {
	return r.Executor(), upstream.AttemptInput{Protocol: wire, Body: mapQoderRequestModel(value, body), ResponseModel: responseModel, Stream: qoder.GjsonBool(body, "stream"), Target: r.Target(metadata, value)}
}

// Target binds provider metadata plus callbacks for attributes, session
// credentials, client selection, and request transport. Site parse errors use
// Global in metadata initially; production client construction reparses the
// provider and returns site errors when invoked.
//
// A supplied nonproduction StreamClient is reused for fixtures or custom
// execution; a concrete Qoder client is rebuilt from the current site profile.
// Attribute lookup reuses request sessions or creates one from the bound
// service. Callers keep the borrowed provider record stable while those
// callbacks resolve its credentials, site, proxy, and model identity.
func (r *QoderRuntime) Target(metadata qoder.RequestMetadata, value *provider.Record) *qoder.Target {
	site, err := qoderRuntimeSite(value)
	// Metadata remains constructible when site parsing fails; the production client
	// callback reparses and returns that error before it can choose a site profile.
	if err != nil {
		site = qoder.SiteGlobal
	}
	id := int64(0)
	if value != nil {
		id = value.ID
	}
	return &qoder.Target{
		ProviderID: id, Site: site, Metadata: metadata,
		Attributes: func(ctx context.Context, model string, wire protocol.ProtocolID) (modelrules.Resolution, error) {
			if _, err := value.QoderModelIndex(); err != nil {
				return modelrules.Resolution{}, &qoder.APIError{StatusCode: 503, Code: "model_catalog_unavailable", Message: err.Error()}
			}
			target := value.ModelAttributeRoute(model, string(wire), string(protocol.ProtocolQoderChat))
			session := routing.ModelAttributesFromContext(ctx)
			if session == nil {
				session = r.options.Attributes.NewSession()
			}
			var groupID int64
			if group, ok := requeststate.GroupFromContext(ctx); ok && group != nil {
				groupID = group.ID
			}
			return session.Resolve(ctx, groupID, target)
		},
		Session: func(ctx context.Context) (*qoder.SessionContext, error) {
			return r.options.Tokens.GetSession(ctx, value)
		},
		Client: func() (qoder.StreamClient, error) {
			if r.options.Client != nil {
				if _, production := r.options.Client.(*qoder.Client); !production {
					return r.options.Client, nil
				}
			}
			resolved, err := qoderRuntimeSite(value)
			if err != nil {
				return nil, err
			}
			profile, err := qoder.ProfileForSite(resolved)
			if err != nil {
				return nil, err
			}
			return qoder.NewClientForProfile(profile), nil
		},
		Doer: provideradapter.QoderRequestDoer(value, r.options.Transport, r.options.Profiles),
	}
}

// ObserveQoderFailure passes a provider-attributed execution error to the
// shared Qoder health observer. Nil runtime or provider returns immediately;
// the adapter decides how API failure classifications affect persistent state.
func (r *QoderRuntime) ObserveQoderFailure(ctx context.Context, value *provider.Record, err error) {
	if r == nil || value == nil {
		return
	}
	provideradapter.ObserveQoderUpstreamError(ctx, value.ID, r.options.Health, err)
}

// qoderRuntimeSite parses the provider credential site using Qoder platform
// rules. A nil record returns Global with an error, allowing callers to choose
// metadata recovery while execution still reports missing provider
// configuration.
func qoderRuntimeSite(value *provider.Record) (qoder.Site, error) {
	if value == nil {
		return qoder.SiteGlobal, fmt.Errorf("qoder: provider is nil")
	}
	return qoder.ParseSite(value.GetCredential("site"))
}

// mapQoderRequestModel applies one nonempty effort-aware provider mapping to a
// Qoder request model. Nil or non-Qoder records, empty bodies/models, and
// unchanged mappings return the supplied bytes; successful mapping uses the
// protocol byte replacement helper.
func mapQoderRequestModel(value *provider.Record, body []byte) []byte {
	if value == nil || !value.IsQoder() || len(body) == 0 {
		return body
	}
	model := strings.TrimSpace(qoder.GjsonString(body, "model"))
	if model == "" {
		return body
	}
	effort := requeststate.ExtractRoutingReasoningEffort(body, "")
	mapped, matched, _ := provider.ResolveMappedModelForEffort(
		provider.ResolveModelMapping(value, provideradapter.ModelDefaults()),
		provider.ResolveEffortModelMapping(value),
		model,
		effort,
	)
	if !matched || mapped == "" || mapped == model {
		return body
	}
	return openaiwire.ReplaceModelInBody(body, mapped)
}
