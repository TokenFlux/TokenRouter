package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// routing_consumers.go adapts routing groups for API-Key policy, identity
// administration, and localized subscription display. These consumers read
// different views from the same routing repository. Key conversion uses
// apikey's group copying rules, identity selects administrator fields,
// subscription display resolves group text in the request's language, and plan
// writes check that declared group identifiers still resolve.

// keyGroups adapts routing group reads for API-Key policy consumers.
type keyGroups struct {
	Repository routing.GroupRepository // shared routing repository
}

// identityAdminGroups adapts routing group reads for identity administration.
type identityAdminGroups struct {
	Repository routing.GroupRepository // shared routing repository
}

// billingGroups adapts localized routing group names for subscription display.
type billingGroups struct {
	Repository routing.GroupRepository // shared routing repository
}

// planGroupValidator checks plan group selections against stored groups through the routing
// repository that owns group existence.
type planGroupValidator struct {
	lookup routing.GroupExistenceLookup // group existence reader
}

// GetByID returns the routing record through API-Key group conversion.
func (p keyGroups) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	v, e := p.Repository.GetByID(ctx, id)
	return apikey.GroupFromRouting(v), e
}

// GetByIDLite converts a lightweight routing read for API-Key policy.
func (p keyGroups) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	v, e := p.Repository.GetByIDLite(ctx, id)
	return apikey.GroupFromRouting(v), e
}

// ListActive converts active routing groups using API-Key copying rules. Nil
// repository results stay nil, while available groups are converted into a new
// outer slice beside the read error.
func (p keyGroups) ListActive(ctx context.Context) ([]routing.Group, error) {
	v, e := p.Repository.ListActive(ctx)
	if v == nil {
		return nil, e
	}
	out := make([]routing.Group, len(v))
	for i := range v {
		out[i] = *apikey.GroupFromRouting(&v[i])
	}
	return out, e
}

// keyGroupFastPolicy resolves the raw Fast value and legacy force flag using
// routing rules.
func keyGroupFastPolicy(raw string, force bool) string {
	return (&routing.Group{OpenAIFastPolicy: raw, ForceOpenAIFast: force}).EffectiveOpenAIFastPolicy()
}

// GetByID narrows the full routing group read for identity administration.
func (p identityAdminGroups) GetByID(ctx context.Context, id int64) (*identity.AdminGroup, error) {
	v, e := p.Repository.GetByID(ctx, id)
	return identityAdminGroup(v), e
}

// GetByIDLite narrows the lightweight group read for identity administration.
func (p identityAdminGroups) GetByIDLite(ctx context.Context, id int64) (*identity.AdminGroup, error) {
	v, e := p.Repository.GetByIDLite(ctx, id)
	return identityAdminGroup(v), e
}

// identityAdminGroup returns nil for absence or a selected administrator group
// view. ID, name, status, exclusivity, and RPM remain the stored values used by
// user administration.
func identityAdminGroup(v *routing.Group) *identity.AdminGroup {
	if v == nil {
		return nil
	}
	return &identity.AdminGroup{ID: v.ID, Name: v.Name, Status: v.Status, IsExclusive: v.IsExclusive, RPMLimit: v.RPMLimit}
}

// ValidatePlanGroupIDs reports a missing-resource error when any supplied identifier does not
// resolve to a stored, undeleted group, so a plan cannot keep covering a deleted group.
func (v planGroupValidator) ValidatePlanGroupIDs(ctx context.Context, ids []int64) error {
	return routing.ValidateGroupIDs(ctx, v.lookup, ids)
}

// GetByIDLite returns the group ID and localized name for subscription display.
// Missing repository or group produces nil, read errors are retained, and
// GroupDisplay uses the request context's language.
func (b billingGroups) GetByIDLite(ctx context.Context, id int64) (*billing.SubscriptionPlanGroup, error) {
	if b.Repository == nil {
		return nil, nil
	}
	g, err := b.Repository.GetByIDLite(ctx, id)
	if err != nil || g == nil {
		return nil, err
	}
	// Subscription responses use the group copy for the request language.
	display, _ := routing.GroupDisplay(g, locale.FromContext(ctx))
	return &billing.SubscriptionPlanGroup{ID: g.ID, Name: display.DisplayName}, nil
}
