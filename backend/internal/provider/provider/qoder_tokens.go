package provider

import (
	"context"
	"errors"
	"sync"
	"time"

	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoder_tokens.go adapts provider credentials and transport to cached Qoder
// COSY session construction. Qoder COSY sessions can be built from a PAT or a
// stored direct token identity. This adapter selects credential strings and
// provider transport for the site builder, while the core session service
// controls credential generations, shared builds, cancellation and cached
// session expiry.

// errQoderSessionBuildInvalidated aliases the core error returned when a
// shared build loses its credential generation.
var errQoderSessionBuildInvalidated = providercore.ErrQoderSessionBuildInvalidated

type (
	// qoderPATExchanger exchanges Global PAT input for its site identity.
	qoderPATExchanger func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error)
	// qoderCNPATExchanger exchanges CN PAT input and returns its token expiry.
	qoderCNPATExchanger func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error)
	// qoderOrganizationTagsGetter fetches organization metadata for a token
	// and organization.
	qoderOrganizationTagsGetter func(ctx context.Context, token, organizationID string) (*qoder.OrganizationTags, error)
)

// qoderSessionCacheEntry aliases the core cache entry holding a Qoder session
// pointer.
type qoderSessionCacheEntry = providercore.QoderSessionCacheEntry[*qoder.SessionContext]

// QoderTokenProvider builds and caches COSY sessions for Qoder providers.
type QoderTokenProvider struct {
	// Core and coreOnce retain one initialized session owner. Configure Core before
	// first use; qoderState creates it once when absent and later calls reuse the
	// same owner for cache, invalidation and stop operations.
	Core     *qoderSessionState // shared session state owner
	coreOnce sync.Once          // lazy core initialization gate
	// exchangePAT optionally replaces Global PAT exchange in each session builder.
	// Nil selects the upstream builder default with this provider request doer.
	exchangePAT qoderPATExchanger
	// exchangeCNPAT optionally replaces CN PAT exchange in each session builder.
	// Nil selects the upstream CN flow and its returned session expiry.
	exchangeCNPAT qoderCNPATExchanger
	// getOrgTags optionally supplies organization discovery while the upstream
	// builder
	// prepares an identity. Nil uses the builder default when metadata is missing.
	getOrgTags qoderOrganizationTagsGetter
	// httpUpstream and tlsFPProfileService are setup-time request
	// dependencies. Set them
	// before concurrent session reads; sessionBuilder combines them for each
	// provider.
	httpUpstream        QoderTransport             // shared provider request transport
	tlsFPProfileService *egressadapter.TLSProfiles // optional TLS fingerprint resolver
}

// qoderSessionState aliases generation-aware core session storage for Qoder
// session pointers.
type qoderSessionState = providercore.QoderSessions[*qoder.SessionContext]

// NewQoderTokenProvider allocates core session state and retains builder overrides.
// Only the PAT and organization callbacks are retained from builder. Configure
// the provider transport separately through SetHTTPUpstream; construction sets the
// core's default shared build timeout and starts no build.
func NewQoderTokenProvider(builder qoder.SessionBuilder) *QoderTokenProvider {
	return &QoderTokenProvider{
		Core: &qoderSessionState{
			Sessions:            make(map[int64]qoderSessionCacheEntry),
			ProviderStates:      make(map[int64]providercore.QoderSessionProviderState),
			SessionBuildTimeout: providercore.DefaultQoderSessionBuildTimeout,
		},
		exchangePAT:   qoderPATExchanger(builder.ExchangePAT),
		exchangeCNPAT: qoderCNPATExchanger(builder.ExchangeCNPAT),
		getOrgTags:    qoderOrganizationTagsGetter(builder.GetOrgTags),
	}
}

// SetHTTPUpstream installs the transport and TLS profile service used by
// future builders.
// A nil receiver is ignored. Call it during setup before concurrent session
// requests, because these dependency fields are assigned without synchronization.
func (p *QoderTokenProvider) SetHTTPUpstream(httpUpstream QoderTransport, tlsFPProfileService *egressadapter.TLSProfiles) {
	if p == nil {
		return
	}
	p.httpUpstream = httpUpstream
	p.tlsFPProfileService = tlsFPProfileService
}

// GetSession obtains or builds the Qoder session through shared core state.
// A nil receiver returns an error. The core validates provider kind, shares
// builds by identity/generation and detaches shared work from individual waiter
// cancellation. Supply a nonnil context and keep the record stable through
// snapshot capture. Returned session pointers can be reused by later readers,
// so callers synchronize mutations of their contents.
//
// A warm cache can return its session even when ctx is already canceled. Cache
// misses check caller cancellation before launching or joining a shared build.
func (p *QoderTokenProvider) GetSession(ctx context.Context, provider *providercore.Record) (*qoder.SessionContext, error) {
	if p == nil {
		return nil, errors.New("qoder token provider is nil")
	}
	return p.qoderState().GetSession(ctx, provider, func(ctx context.Context, value *providercore.Record) (*qoder.SessionContext, time.Time, error) {
		return p.buildSession(ctx, value)
	})
}

// Invalidate evicts the provider session and advances its core credential
// generation.
// A nil receiver is ignored. An older in-flight build then cannot install its
// result as the current cache entry for that provider.
func (p *QoderTokenProvider) Invalidate(providerID int64) {
	if p != nil {
		p.qoderState().Invalidate(providerID)
	}
}

// InvalidateProvider supplies a refreshed record to the core's identity-aware
// invalidation.
// Nil receiver is ignored. The core clones supported record containers, rejects
// older credential snapshots, and advances the generation when it accepts the
// new observed identity.
func (p *QoderTokenProvider) InvalidateProvider(provider *providercore.Record) {
	if p != nil {
		p.qoderState().InvalidateProvider(provider)
	}
}

// qoderState returns the retained core, allocating it once if missing.
// A nonnil receiver is required. Core configuration belongs before first use;
// the sync.Once gate protects allocation, while session coordination belongs
// to the returned core.
func (p *QoderTokenProvider) qoderState() *qoderSessionState {
	p.coreOnce.Do(func() {
		if p.Core == nil {
			p.Core = &qoderSessionState{}
		}
	})
	return p.Core
}

// StopContext asks the core to cancel shared builds and wait within ctx.
// A nil receiver returns nil. Stopped state rejects new work and late cache
// publication, and a waiting timeout reports that admitted builds have not
// finished. Supply a nonnil ctx when waiting for active work or another
// shutdown caller.
func (p *QoderTokenProvider) StopContext(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.qoderState().StopContext(ctx)
}

// QoderCredentialInput copies selected credential strings into the upstream
// input shape.
// A nil record returns nil. Provider name and the selected PAT, token, machine,
// site and organization hints are read through GetCredential, so mutable record
// maps are not passed directly into the builder.
func QoderCredentialInput(value *providercore.Record) *qoder.CredentialInput {
	if value == nil {
		return nil
	}
	return &qoder.CredentialInput{
		Name:               value.Name,
		Pat:                value.GetCredential("pat"),
		Site:               value.GetCredential("site"),
		RefreshMode:        value.GetCredential("refresh_mode"),
		SecurityOauthToken: value.GetCredential("security_oauth_token"),
		MachineId:          value.GetCredential("machine_id"),
		MachineToken:       value.GetCredential("machine_token"),
		MachineType:        value.GetCredential("machine_type"),
		Uid:                value.GetCredential("uid"),
		Aid:                value.GetCredential("aid"),
		IdentityName:       value.GetCredential("name"),
		UserType:           value.GetCredential("user_type"),
		RefreshToken:       value.GetCredential("refresh_token"),
		OrganizationId:     value.GetCredential("organization_id"),
		OrganizationName:   value.GetCredential("organization_name"),
	}
}

// sessionBuilder combines stored callback overrides with one provider's
// request doer.
// It returns a new builder for the call, leaving retained callbacks intact.
// Proxy and TLS selection are supplied by QoderRequestDoer from the current
// provider dependencies.
func (p *QoderTokenProvider) sessionBuilder(value *providercore.Record) *qoder.SessionBuilder {
	return &qoder.SessionBuilder{ExchangePAT: qoder.PATExchanger(p.exchangePAT), ExchangeCNPAT: qoder.CNPATExchanger(p.exchangeCNPAT), GetOrgTags: qoder.OrganizationTagsGetter(p.getOrgTags), Doer: QoderRequestDoer(value, p.httpUpstream, p.tlsFPProfileService)}
}

// buildSession delegates site session construction with the selected
// credential input.
// PAT sessions use the configured exchange callbacks or platform defaults; direct
// token sessions use supplied machine and user identity. The returned expiry
// belongs to the site builder and controls core cache lifetime.
func (p *QoderTokenProvider) buildSession(ctx context.Context, value *providercore.Record) (*qoder.SessionContext, time.Time, error) {
	return p.sessionBuilder(value).BuildSession(ctx, QoderCredentialInput(value))
}
