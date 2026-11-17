package provider

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// candidates.go adapts provider records into batch-image candidates with model
// matching and execution binding.
// Batch model discovery reuses prepared model mappings and eligibility rules
// while routing checks use a captured provider snapshot. Candidate construction
// retains the source provider record for account rules, attribute targets, and
// later execution binding, so callers keep that record stable throughout the
// candidate's use.
//
// The submission callback reads captured routing fields, while attribute
// lookup resolves its protocol from the retained record. SupportsSubmission and
// bound execution receive CloneRecord views for their provider operations.
// Upstream model resolution uses the prepared one-hop mapping and reports the
// trimmed result to an optional observer, keeping discovery and submitted model
// spelling tied to that prepared configuration.

// CandidateProviders reads provider records by identity and enumerates
// schedulable candidates for platform-scoped batch queries.
type CandidateProviders interface {
	ResultProviders // provider identity read methods
	// ListSchedulableByPlatform returns schedulable records for the requested
	// platform and its read error. Candidates converts the returned records into
	// query candidates whose callbacks retain those record values.
	ListSchedulableByPlatform(context.Context, string) ([]provider.Record, error)
	// ListSchedulableByGroupIDAndPlatform returns schedulable records matching a
	// group and platform, with its read error. Candidates retains those records
	// for prepared model rules and later execution callbacks.
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]provider.Record, error)
}

// Candidates prepares provider records for batch model discovery and execution
// binding through a shared adapter registry.
type Candidates struct {
	// Source is required for provider reads. Returned records are retained by
	// prepared candidates, so the source and callers keep their contents stable
	// through subsequent query and execution binding use.
	Source CandidateProviders // provider query record source
	// Registry supplies adapters used by CanSubmit and Bind callbacks.
	// A candidate's requested platform must exist before binding operations;
	// callers keep registered adapters available during their use.
	Registry *batchimage.Registry[BatchImageProvider] // batch platform adapter registry
	// ObserveModel is called when ResolveUpstream computes a trimmed model
	// name. Nil disables observation; callers configure the callback before sharing
	// Candidates so concurrent queries read stable callback state.
	ObserveModel func(context.Context, string) // optional resolved-model observation callback
}

// candidateRules exposes prepared model matching while retaining the provider
// record used by account eligibility checks.
type candidateRules struct {
	// Record and models describe the same prepared candidate. Model indexes are
	// captured, while account checks can still read Record, so that record stays
	// stable for the query's lifetime.
	*provider.Record                              // retained provider eligibility record
	models           *provider.ModelRulesSnapshot // prepared model matching snapshot
}

// GetByID reads a provider and returns its prepared candidate with the source
// error. A nil source record produces a nil candidate; a returned record is
// retained for the candidate's callbacks.
func (r *Candidates) GetByID(ctx context.Context, id int64) (*batchimage.Candidate, error) {
	value, err := r.Source.GetByID(ctx, id)
	return r.Project(value), err
}

// ListSchedulableByPlatform prepares candidates from the platform query and
// returns its source error unchanged. The resulting slice is newly allocated;
// candidate callbacks retain the returned record values for later rule checks.
func (r *Candidates) ListSchedulableByPlatform(ctx context.Context, platform string) ([]batchimage.Candidate, error) {
	values, err := r.Source.ListSchedulableByPlatform(ctx, platform)
	return r.project(values), err
}

// ListSchedulableByGroupIDAndPlatform prepares candidates from the group and
// platform query, retaining its source error. The resulting slice is newly
// allocated, while callbacks retain the record values read for that query.
func (r *Candidates) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, platform string) ([]batchimage.Candidate, error) {
	values, err := r.Source.ListSchedulableByGroupIDAndPlatform(ctx, id, platform)
	return r.project(values), err
}

// project prepares one candidate per supplied provider record in input order.
// It allocates a result slice, including for empty input; callers keep the
// source record values stable because candidate callbacks retain them.
func (r *Candidates) project(values []provider.Record) []batchimage.Candidate {
	out := make([]batchimage.Candidate, len(values))
	for i := range values {
		out[i] = *r.Project(&values[i])
	}
	return out
}

// Project builds a candidate with prepared model rules, captured routing
// fields, and provider-operation callbacks. Nil input returns nil; callers keep
// the retained record and observer state stable while the candidate or its
// bound execution operations remain in use.
//
// CanSubmit uses the captured routing snapshot, whereas attribute lookup
// resolves the retained record's current batch protocol. Provider support checks
// clone the record, and Bind retains it for per-operation cloning. Upstream
// resolution trims the prepared one-hop mapped name and optionally observes
// that same result before returning it.
func (r *Candidates) Project(value *provider.Record) *batchimage.Candidate {
	if value == nil {
		return nil
	}
	models := provider.PrepareModelRules(value, provideradapter.ModelDefaults(), provideradapter.ModelRules(value))
	// Capture routing fields alongside model preparation before constructing the
	// callbacks. The source record remains borrowed for account and attribute
	// checks, so callers keep those inputs stable with this captured configuration.
	snapshot := value.RoutingSnapshot()
	result := &batchimage.Candidate{ID: value.ID, Priority: value.Priority, CandidateRules: candidateRules{Record: value, models: models}}
	result.AttributeTarget = func(model string) modelrules.Target {
		// Attribute lookup uses the resolved upstream protocol field independently
		// of route admission. CanSubmit performs the availability check when the
		// candidate is tested for the batch route.
		protocol, _ := resolveBatchProtocol(value)
		return value.ModelAttributeTarget(model, string(protocol))
	}
	// Resolve protocol admission before adapter-specific new-job checks. Existing
	// jobs bind through ResultAccess, whose credential checks do not depend on
	// current submission permission or new-job storage configuration.
	result.CanSubmit = func(name string) bool {
		if _, ok := routing.Plan(routing.PlanInput{ClientProtocol: protocol.ProtocolImageBatches}).ResolveCandidate(snapshot); !ok {
			return false
		}
		selected, ok := r.Registry.Get(name)
		return ok && selected != nil && selected.SupportsSubmission(provider.CloneRecord(value))
	}
	result.Bind = func(name string) batchimage.ExecutionProvider {
		selected, _ := r.Registry.Get(name)
		return BindProvider(selected, value)
	}
	// Resolve forwarding through the prepared Gemini/Vertex model rules.
	// Observe and return the same trimmed mapped name so submitted model identity
	// matches the candidate query's selected upstream spelling.
	result.ResolveUpstream = func(ctx context.Context, model string) string {
		resolved := strings.TrimSpace(models.ForwardMappedModel(model))
		if r.ObserveModel != nil {
			r.ObserveModel(ctx, resolved)
		}
		return resolved
	}
	return result
}

// GetModelMapping returns a copied map of captured model mappings.
// The prepared models snapshot is required; callers may edit this result
// without changing the mapping used by subsequent candidate checks.
func (r candidateRules) GetModelMapping() map[string]string {
	return r.models.Mapping()
}

// IsModelSupported checks prepared model scope and retained account
// eligibility for the supplied model. A missing snapshot rejects admission;
// the record and platform callback state remain stable during candidate use.
func (r candidateRules) IsModelSupported(model string) bool {
	return r.models.Supports(model)
}

// ResolveMappedModel returns the prepared one-hop model target and whether a
// rule matched. A prepared snapshot is required; unmatched input retains its
// spelling, and a matched empty target still reports true.
func (r candidateRules) ResolveMappedModel(model string) (string, bool) {
	return r.models.ResolveMappedModel(model)
}

// GetConfiguredRequestModels returns a copied slice of sorted concrete model
// names captured from configuration. A prepared snapshot is required; platform
// and protocol admission still run when those candidate names are queried.
func (r candidateRules) GetConfiguredRequestModels() []string {
	return r.models.ConfiguredModels()
}
