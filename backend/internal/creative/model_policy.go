package creative

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// model_policy.go resolves creative model candidates through group
// restrictions, provider mappings, and operation capabilities.
// Creative directory names come from finite configuration candidates rather
// than the entire model metadata catalogue. Group mappings and provider rules
// resolve each candidate to a concrete upstream image route, with restrictions
// applied at their configured requested, group-mapped, or upstream stage.
// Wildcard settings cannot themselves become request names, but wildcard rules
// can match the finite candidates.
//
// A query reads each eligible platform provider list once and shares a
// model-attribute session across resolutions. An available route is selected
// in OpenAI, Gemini, then Grok order, combining capabilities across eligible
// providers and separately across each operation. Display resolution can
// continue after attribute errors using name recognition, while submission
// resolution returns those errors before the task is created.

// groupModelPolicy snapshots group admission rules for one query or execution
// attempt, independently of pricing.
type groupModelPolicy struct {
	view *routing.GroupPolicyView // cloned enabled group policy view
}

// creativeModelQuery reuses provider records and model rules for one group.
type creativeModelQuery struct {
	// query shared model attribute session
	attributes *routing.ModelAttributeSession
	// group retains the supplied view while policy clones its routing policy.
	// Callers keep group operation and fallback maps stable throughout this query.
	group  *GroupView       // supplied group view pointer
	policy groupModelPolicy // cloned group model restriction policy
	// queried eligible providers by platform
	providers map[string][]CatalogProvider
}

// newGroupModelPolicy clones an enabled group routing policy for creative
// admission. A disabled policy yields a nil view, whose routing methods
// preserve the requested model and allow unrestricted admission.
func newGroupModelPolicy(policy routing.GroupRoutingPolicy) groupModelPolicy {
	if !policy.Enabled {
		return groupModelPolicy{}
	}
	return groupModelPolicy{view: &routing.GroupPolicyView{
		GroupRoutingPolicy: policy.Clone(),
	}}
}

// resolve returns the single group-mapped model when the configured
// restriction stage permits it. Upstream-stage restrictions are deferred until
// provider model resolution supplies the actual model being sent.
func (p groupModelPolicy) resolve(requested string) (string, bool) {
	mapped := p.view.ResolveModel(requested)
	if p.view.RestrictionSource() != routing.BillingModelSourceUpstream {
		model := routing.ModelForRestriction(p.view.RestrictionSource(), requested, mapped)
		if p.view.IsModelRestricted(model) {
			return "", false
		}
	}
	return mapped, true
}

// allowsUpstream reports whether the final provider model passes an
// upstream-stage group restriction. Other stages were checked during group
// resolution, and a disabled policy permits the model.
func (p groupModelPolicy) allowsUpstream(model string) bool {
	return p.view.RestrictionSource() != routing.BillingModelSourceUpstream || !p.view.IsModelRestricted(model)
}

// candidates returns sorted concrete names collected from supplied
// configuration, group policy, and schedulable providers. Blank names and
// names containing wildcard characters are omitted.
//
// Both source and target names in group mappings contribute finite candidates,
// while providers contribute configured request names and mapping sources.
// Candidate inclusion alone grants no route permission: resolve later checks
// group restrictions, provider support, protocols, and image capability before
// a name reaches the catalogue.
func (p groupModelPolicy) candidates(configured []string, providers []CatalogProvider) []string {
	models := make(map[string]struct{})
	add := func(values ...string) {
		for _, model := range values {
			model = strings.TrimSpace(model)
			// Rules may contain wildcard patterns, but the
			// directory submits concrete names.
			// Keep patterns out of this finite candidate set so
			// route matching, rather
			// than a pattern string itself, determines each public entry.
			if model != "" && !strings.ContainsAny(model, "*?") {
				models[model] = struct{}{}
			}
		}
	}
	add(configured...)
	if p.view != nil {
		add(p.view.AllowedModels...)
		for source, target := range p.view.ModelMapping {
			add(source, target)
		}
	}
	for _, provider := range providers {
		if provider == nil || !provider.IsSchedulable() {
			continue
		}
		add(provider.GetConfiguredRequestModels()...)
		for model := range provider.GetModelMapping() {
			add(model)
		}
	}
	result := make([]string, 0, len(models))
	for model := range models {
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}

// prepareCreativeModels reads eligible providers once for each platform with
// group operations. Missing provider readers, nil groups, or ClaudeCodeOnly
// groups return a nil query; provider read errors abort preparation.
func (s *Public) prepareCreativeModels(ctx context.Context, group *GroupView) (*creativeModelQuery, error) {
	if s.ProviderRepo == nil || group == nil || group.ClaudeCodeOnly {
		return nil, nil
	}
	query := &creativeModelQuery{group: group, policy: newGroupModelPolicy(group.RoutingPolicy), providers: map[string][]CatalogProvider{}}
	query.attributes = s.Attributes.NewSession()
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformGrok} {
		if len(group.Operations[platform]) == 0 {
			continue
		}
		providers, err := s.ProviderRepo.ListSchedulableByGroupIDAndPlatform(ctx, group.ID, platform)
		if err != nil {
			return nil, err
		}
		query.providers[platform] = providers
	}
	return query, nil
}

// creativeSettingsModels returns model names configured for the exact supplied
// group identifier.
func creativeSettingsModels(settings []CreativeModelSetting, groupID int64) []string {
	var models []string
	for _, setting := range settings {
		if setting.GroupID == groupID {
			models = append(models, setting.Model)
		}
	}
	return models
}

// candidates combines configured names with the supplied group custom model
// list. A nil query returns nil, and the cloned policy filters and sorts
// candidates across the prepared platform providers.
func (q *creativeModelQuery) candidates(configured []string) []string {
	if q == nil {
		return nil
	}
	var providers []CatalogProvider
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformGrok} {
		providers = append(providers, q.providers[platform]...)
	}
	models := append([]string(nil), configured...)
	models = append(models, q.group.ModelsList...)
	return q.policy.candidates(models, providers)
}

// resolveModels returns routes for distinct input names that resolve
// successfully. Cancellation returns the context error before each name,
// unavailable routes are omitted, and display attribute errors follow resolve
// display handling.
func (q *creativeModelQuery) resolveModels(ctx context.Context, models []string) (map[string]creativeModelRoute, error) {
	out := make(map[string]creativeModelRoute)
	for _, model := range models {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if _, exists := out[model]; exists {
			continue
		}
		if route, ok, err := q.resolve(ctx, model, true); err != nil {
			return nil, err
		} else if ok {
			out[model] = route
		}
	}
	return out, nil
}

// resolve selects the first platform with an eligible image route for a
// concrete request name. Nil queries, cancelled contexts, blank names, and
// wildcard names report unavailable with no error.
//
// Group mapping precedes provider mapping and staged restrictions. Each
// provider operation must pass its protocol route before attribute resolution,
// and final output modalities must explicitly include image. Attribute loading
// failures stop both discovery and submission. Display mode omits routes whose
// loaded attributes lack image output; submission reports their capability
// rejection. Shared capabilities are computed across matching
// providers and per operation, with OpenAI, Gemini, then Grok defining
// platform precedence.
func (q *creativeModelQuery) resolve(ctx context.Context, model string, display bool) (creativeModelRoute, bool, error) {
	if q == nil || ctx.Err() != nil || strings.TrimSpace(model) == "" || strings.ContainsAny(model, "*?") {
		return creativeModelRoute{}, false, nil
	}
	mapped, allowed := q.policy.resolve(model)
	if !allowed {
		return creativeModelRoute{}, false, nil
	}
	var capabilityErr error
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformGrok} {
		route := creativeModelRoute{Platform: platform, OperationAttributes: map[string]modelcatalog.Attributes{}}
		var values []modelcatalog.Attributes
		perOperation := map[string][]modelcatalog.Attributes{}
		for _, provider := range q.providers[platform] {
			if provider == nil || provider.PlatformID() != platform || !provider.IsSchedulable() || !provider.IsModelSupported(mapped) {
				continue
			}
			finalModel := mappedCatalogModel(provider, mapped)
			if !q.policy.allowsUpstream(finalModel) {
				continue
			}
			for _, operation := range q.group.Operations[platform] {
				if !provider.AllowsProtocol(OperationProtocol(platform, operation), q.group.ProtocolFallbacks) {
					continue
				}
				target := modelcatalog.Target{Platform: platform, Model: finalModel}
				if aware, ok := provider.(interface {
					// AttributeTarget supplies
					// provider-specific identity for
					// selected model attributes.
					AttributeTarget(string) modelcatalog.Target
				}); ok {
					target = aware.AttributeTarget(finalModel)
				}
				target.ClientProtocol = string(OperationProtocol(platform, operation))
				target.UpstreamProtocol = target.ClientProtocol
				resolved, err := q.attributes.Resolve(ctx, q.group.ID, target)
				if err != nil {
					return creativeModelRoute{}, false, err
				}
				if err := routing.RequireImageOutput(resolved.Attributes); err != nil {
					capabilityErr = err
					continue
				}
				if resolved.Attributes.Media != nil {
					formats := []string{}
					if operation != CreativeOperationInpaint && provider.SupportsImageGIF(OperationProtocol(platform, operation)) && slices.Contains(resolved.Attributes.Media.OutputFormats, "gif") {
						formats = []string{"png", "gif"}
					}
					resolved.Attributes = modelcatalog.Merge(resolved.Attributes, modelcatalog.Attributes{Media: &modelcatalog.MediaCapabilities{OutputFormats: formats}})
				}
				if route.Model == "" {
					route.Model = finalModel
				}
				if !slices.Contains(route.Operations, operation) {
					route.Operations = append(route.Operations, operation)
				}
				perOperation[operation] = append(perOperation[operation], resolved.Attributes)
				values = append(values, resolved.Attributes)
			}
		}
		if len(route.Operations) > 0 {
			route.Attributes = creativeCommonAttributes(values)
			for operation, attributes := range perOperation {
				route.OperationAttributes[operation] = creativeCommonAttributes(attributes)
			}
			return route, true, nil
		}
	}
	if !display && capabilityErr != nil {
		return creativeModelRoute{}, false, capabilityErr
	}
	return creativeModelRoute{}, false, nil
}

// creativeCommonAttributes keeps common controls and unions selectable GIF output.
// GIF selection adds a request eligibility requirement, so only providers with
// that format execute it. Static controls retain the existing common-route rules.
func creativeCommonAttributes(values []modelcatalog.Attributes) modelcatalog.Attributes {
	attrs, _ := modelcatalog.Common(values)
	for _, value := range values {
		if value.Media != nil && slices.Contains(value.Media.OutputFormats, "gif") {
			if attrs.Media == nil {
				attrs.Media = &modelcatalog.MediaCapabilities{}
			}
			attrs.Media.OutputFormats = []string{"png", "gif"}
			break
		}
	}
	return attrs
}
