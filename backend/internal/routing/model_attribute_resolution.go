package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// model_attribute_resolution.go resolves route attributes against catalogue
// and group-profile snapshots retained for a request.
// A request can retry providers or switch protocols while model metadata is
// published independently. Its attribute session pins one catalogue View, reads
// each group's profile once, and resolves later attempts against that retained
// input.
//
// A single mutex protects first profile reads and route-cache publication,
// including the repository call and JSON copying. Read or copy failures remain
// cached so later attempts cannot switch to a different configuration
// mid-request. Cached resolutions use complete value-based target keys and
// detached copies; a newly computed result can still retain its input target's
// output-cap pointer. Renewing a session lets the next request or WebSocket
// turn observe a new catalogue and profile.

// modelAttributeContextKey identifies the request-local attribute session in
// context values.
type modelAttributeContextKey struct{}

// ModelAttributeMatch narrows an override to routes matching every populated
// field.
type ModelAttributeMatch struct {
	// optional provider platform selector
	Platform string `json:"platform,omitempty"`
	// optional provider authentication selector
	ProviderType string `json:"provider_type,omitempty"`
	// optional payg or coding selector
	ProviderMode string `json:"provider_mode,omitempty"`
	Site         string `json:"site,omitempty"` // optional regional site selector
	// optional client entry selector
	ClientProtocol string `json:"client_protocol,omitempty"`
	// optional upstream protocol selector
	UpstreamProtocol string `json:"upstream_protocol,omitempty"`
}

// attributeGroupResult retains one group's loaded configuration, failure, and
// resolved route cache.
type attributeGroupResult struct {
	// config and err retain the first profile load and JSON copy result.
	// resolutions uses that retained profile until a new session is created,
	// including after a failure.
	config *ModelAttributeConfig // retained copied group profile
	err    error                 // retained profile loading failure
	// detached resolutions by route value
	resolutions map[modelrules.TargetKey]modelrules.Resolution
}

// ModelAttributeSession pins a catalogue view and serializes group profile
// loading across request attempts.
type ModelAttributeSession struct {
	service *ModelAttributeService // service creating this session
	view    modelrules.View        // pinned catalogue publication view
	// mu protects groups, including repository reads and profile copying on the
	// first access. Resolve serializes route computation under this same lock
	// before publishing cache entries.
	mu     sync.Mutex                     // profile and route-cache mutex
	groups map[int64]attributeGroupResult // profile results by group identifier
}

// Matches reports whether every populated selector equals its target field
// exactly. A nil receiver or empty selector matches all routes; callers supply
// selector spellings consistent with the resolved target values.
func (m *ModelAttributeMatch) Matches(target modelrules.Target) bool {
	if m == nil {
		return true
	}
	for _, pair := range [][2]string{{m.Platform, target.Platform}, {m.ProviderType, target.ProviderType}, {m.ProviderMode, target.ProviderMode}, {m.Site, target.Site}, {m.ClientProtocol, target.ClientProtocol}, {m.UpstreamProtocol, target.UpstreamProtocol}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return false
		}
	}
	return true
}

// Validate checks populated route selectors against declared platform,
// provider, mode, site, and protocol values. Nil receivers are valid, and each
// field is checked independently rather than validating complete provider
// combinations.
func (m *ModelAttributeMatch) Validate() error {
	if m == nil {
		return nil
	}
	if m.Platform != "" && !slices.Contains([]string{"openai", "anthropic", "gemini", "antigravity", "grok", "qoder", "kimi", "zhipu", "deepseek", "jev"}, m.Platform) {
		return fmt.Errorf("unknown platform %q", m.Platform)
	}
	if m.ProviderType != "" && !slices.Contains([]string{"apikey", "oauth", "setup-token", "bedrock", "service_account", "cosy", "upstream"}, m.ProviderType) {
		return fmt.Errorf("unknown provider type %q", m.ProviderType)
	}
	if m.ProviderMode != "" && m.ProviderMode != "payg" && m.ProviderMode != "coding" {
		return fmt.Errorf("provider mode must be payg or coding")
	}
	if m.Site != "" && m.Site != "cn" && m.Site != "global" {
		return fmt.Errorf("site must be cn or global")
	}
	for _, value := range []string{m.ClientProtocol, m.UpstreamProtocol} {
		if value == "" {
			continue
		}
		found := false
		for _, item := range capability.ProtocolCatalog() {
			if string(item.ID) == value {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown protocol %q", value)
		}
	}
	return nil
}

// view returns the service's current catalogue View when its provider is
// configured. A single-model reader can supply the base layer instead. Nil
// services retain a zero view for local platform resolution.
func (s *ModelAttributeService) view() modelrules.View {
	if s != nil && s.Catalog.View != nil {
		return s.Catalog.View()
	}
	if s != nil {
		return modelrules.ViewFromLookup(s.Catalog.Lookup)
	}
	return modelrules.View{}
}

// viewForContext reuses a context session's pinned View when that session
// belongs to this service. Sessions from another service are ignored, and the
// current View callback supplies a fresh view.
func (s *ModelAttributeService) viewForContext(ctx context.Context) modelrules.View {
	if session := ModelAttributesFromContext(ctx); session != nil && session.service == s {
		return session.view
	}
	return s.view()
}

// NewSession pins the current catalogue View and allocates empty per-group
// resolution storage. It performs no profile reads; the first Resolve call
// loads the requested group under the session mutex.
func (s *ModelAttributeService) NewSession() *ModelAttributeSession {
	return &ModelAttributeSession{service: s, view: s.view(), groups: map[int64]attributeGroupResult{}}
}

// WithContext attaches this session to a new phase of the same request.
// The destination context keeps its cancellation and deadline. Selection and
// background execution share the pinned catalogue, group profile, and cached
// failures through this session; independent requests create a new session.
func (s *ModelAttributeSession) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, modelAttributeContextKey{}, s)
}

// WithModelAttributes attaches a new attribute session to the supplied context
// and pins its catalogue. Callers create separate sessions for independent
// background work and each WebSocket turn that should observe updates.
func WithModelAttributes(ctx context.Context, service *ModelAttributeService) context.Context {
	return service.NewSession().WithContext(ctx)
}

// ModelAttributesFromContext returns the attribute session stored under the
// private context key. Missing or differently typed values return nil, allowing
// callers to use service-level resolution instead.
func ModelAttributesFromContext(ctx context.Context) *ModelAttributeSession {
	value, _ := ctx.Value(modelAttributeContextKey{}).(*ModelAttributeSession)
	return value
}

// RenewModelAttributes replaces an existing context session with a fresh
// session from its service. Contexts without a session are returned unchanged;
// renewed sessions observe later catalogue and profile reads.
func RenewModelAttributes(ctx context.Context) context.Context {
	if session := ModelAttributesFromContext(ctx); session != nil {
		return WithModelAttributes(ctx, session.service)
	}
	return ctx
}

// CopyModelAttributes attaches source's existing attribute session to target
// when present. The contexts then share the same pinned catalogue, profile
// failures, and route cache; absent sessions leave target unchanged.
func CopyModelAttributes(target, source context.Context) context.Context {
	if session := ModelAttributesFromContext(source); session != nil {
		return context.WithValue(target, modelAttributeContextKey{}, session)
	}
	return target
}

// Resolve returns attributes for a group and complete route using this
// session's pinned catalogue. A missing session or service resolves through a
// zero view, while a positive group ID can load its profile from the
// repository.
//
// The mutex covers loading, JSON copying, and cache updates, so concurrent
// attempts use one stored profile or failure. Load and copy errors become
// MODEL_ATTRIBUTES_UNAVAILABLE and remain cached. Successful resolutions are
// cached as detached clones, and cache hits return another clone. The first
// computed result can retain target's output-cap pointer, so its caller
// controls that input's mutation.
func (s *ModelAttributeSession) Resolve(ctx context.Context, groupID int64, target modelrules.Target) (modelrules.Resolution, error) {
	if s == nil || s.service == nil {
		return (modelrules.View{}).Resolve(target, modelcatalog.Attributes{}), nil
	}
	// Serialize the first repository read with profile copying and route cache
	// publication so simultaneous attempts cannot retain different inputs for one
	// group.
	s.mu.Lock()
	defer s.mu.Unlock()
	group, found := s.groups[groupID]
	if !found {
		group.resolutions = make(map[modelrules.TargetKey]modelrules.Resolution)
		if groupID > 0 && s.service.Repo != nil {
			group.config, group.err = s.service.Repo.ForGroup(ctx, groupID)
			if group.err == nil && group.config != nil {
				// Detach the repository profile through its JSON representation so later
				// stored-value edits cannot alter this request's rules or localization.
				var body []byte
				body, group.err = json.Marshal(group.config)
				if group.err == nil {
					group.config = &ModelAttributeConfig{}
					group.err = json.Unmarshal(body, group.config)
				}
			}
		}
		s.groups[groupID] = group
	}
	if group.err != nil {
		// The retained load or copy failed, so report configuration unavailability on
		// every attempt instead of silently resolving against a different profile.
		return modelrules.Resolution{}, apperror.ServiceUnavailable("MODEL_ATTRIBUTES_UNAVAILABLE", "model attribute configuration is unavailable")
	}
	key := target.Key()
	if result, ok := group.resolutions[key]; ok {
		return result.Clone(), nil
	}
	result := s.service.resolve(s.view, group.config, target, s.service.identityCandidates())
	// Retain detached values before returning the computed result; caller edits to
	// its attributes or input cap cannot modify a later cache-hit resolution.
	group.resolutions[key] = result.Clone()
	return result, nil
}

// resolve detects the target's service identity and merges its matching active
// profile into the supplied catalogue View. A reference resolves the target
// against the same route and restores the actual outbound identity afterward.
// Inactive or absent profiles supply no override.
//
// Full publications and single-model readers supply the same base layer.
// Provider observations and group rules are applied by the retained View.
// The resolution retains the outbound route identity while rule matching can
// expand equivalent resource spellings; callers choose whether returned values
// need additional cloning.
func (s *ModelAttributeService) resolve(view modelrules.View, config *ModelAttributeConfig, target modelrules.Target, candidates func(string) []string) modelrules.Resolution {
	target = target.WithServiceIdentity()
	original := target
	reference := ""
	override := modelcatalog.Attributes{}
	if config != nil && config.Status == StatusActive {
		rule := config.matchRule(target.Model, candidates, target)
		if rule != nil && rule.ReferenceModel != "" {
			reference = rule.ReferenceModel
			target = target.ForReference(reference)
			rule = config.matchRule(target.Model, candidates, target)
			if rule == nil && target.Model != reference {
				rule = config.matchRule(reference, candidates, target)
			}
			if rule != nil && rule.ReferenceModel != "" {
				// Invalid persisted chains remain unknown; using either source or target
				// defaults would hide a configuration that the save API rejects.
				return modelrules.Resolution{Target: original, ReferenceModel: reference, Version: view.Version(), Sources: map[string]string{}}
			}
		}
		if rule != nil {
			override = rule.Attributes
		}
	}
	if reference != "" {
		return view.ResolveReference(original, reference, override)
	}
	return view.Resolve(target, override)
}

// Preview applies a draft or stored profile to already enumerated configured
// routes. Route enumeration precedes capability filtering so a draft can add
// a missing capability and show the same values used by subsequent requests.
func (s *ModelAttributeService) Preview(ctx context.Context, groupID int64, targets []modelrules.Target, draft *ModelAttributeConfig) ([]modelrules.Resolution, error) {
	config := draft
	if draft != nil {
		if err := draft.ValidateReferences(); err != nil {
			return nil, err
		}
	}
	if config == nil && s.Repo != nil {
		var err error
		config, err = s.Repo.ForGroup(ctx, groupID)
		if err != nil {
			return nil, err
		}
	}
	result := make([]modelrules.Resolution, 0, len(targets))
	view := s.viewForContext(ctx)
	candidates := s.identityCandidates()
	for _, target := range targets {
		result = append(result, s.resolve(view, config, target, candidates))
	}
	return result, nil
}

// IsModelAttributeError identifies local capability or attribute configuration failures.
// Callers present these errors through their client protocol and exclude them
// from upstream health feedback because no failed upstream exchange is established.
func IsModelAttributeError(err error) bool {
	switch apperror.Reason(err) {
	case "MODEL_CAPABILITY_UNKNOWN", "MODEL_CAPABILITY_UNSUPPORTED", "MODEL_ATTRIBUTES_UNAVAILABLE":
		return true
	default:
		return false
	}
}

// RequireImageOutput validates declared image output before an image operation.
// Missing declarations and explicit rejection use separate local errors, so
// callers can distinguish incomplete configuration from a known unsupported route.
func RequireImageOutput(attributes modelcatalog.Attributes) error {
	supported := attributes.OutputSupport("image")
	if supported == nil {
		return apperror.BadRequest("MODEL_CAPABILITY_UNKNOWN", "image output capability is unknown; configure model attributes before requesting images")
	}
	if !*supported {
		return apperror.BadRequest("MODEL_CAPABILITY_UNSUPPORTED", "the selected model route does not support image output")
	}
	return nil
}

// Unavailable reports whether the session retains any group profile loading or
// copying error. A nil session returns false, and the mutex provides a
// consistent inspection while route resolution continues.
func (s *ModelAttributeSession) Unavailable() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, group := range s.groups {
		if group.err != nil {
			return true
		}
	}
	return false
}

// identityCandidates obtains the configured model-identity expansion function
// for override matching. An absent provider returns nil, leaving matches based
// on the supplied full model identity alone.
func (s *ModelAttributeService) identityCandidates() func(string) []string {
	if s.Catalog.Candidates != nil {
		return s.Catalog.Candidates()
	}
	return nil
}
