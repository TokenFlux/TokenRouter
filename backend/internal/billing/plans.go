package billing

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
)

// plans.go prepares plan mutations and exposes plan queries through repository
// adapters.
// Plan text updates carry versioned localization alongside plain
// fields used by existing display and checkout readers. When source
// content is supplied, the service derives those plain fields from
// it before validating the request, while the repository owns
// content revision checks and transactional writes.
//
// Creation also merges the legacy group choice with the configured
// list and restricts group rates to the resulting selection. Ordinary
// update pointers and nullable patches keep their distinct preserve,
// clear, and replacement meanings as they reach storage. Deletion
// checks the payment-owned in-progress order count before requesting
// a delete; those separate calls provide a guard but establish no
// transaction or reservation for the plan here.
//
// A declared group selection must name groups that still exist. Deleting a
// group leaves the identifiers that plans recorded, so a plan can otherwise keep
// covering a group that no subscriber can reach, and the display carries the
// identifier with no name. PlanGroupValidator answers that question for the
// service, which checks the selection it is about to store.

// PlanGroupValidator reports whether plan group identifiers name stored groups.
type PlanGroupValidator interface {
	// ValidatePlanGroupIDs returns a missing-resource error when any supplied
	// identifier does not resolve to a stored group. An empty selection needs no
	// lookup; a failure means the service cannot confirm the selection and the
	// plan write does not proceed.
	ValidatePlanGroupIDs(context.Context, []int64) error
}

// PlanRepository reads and persists configured plan terms and localized content.
type PlanRepository interface {
	// ListPlans returns stored plan terms for administration and service
	// readers. The PostgreSQL adapter sorts by configured display order;
	// the facade returns these values as supplied, including localized
	// content available for later resolution.
	ListPlans(context.Context) ([]*SubscriptionPlan, error)
	// ListPlansForSale returns currently offered plan values from storage.
	// Return a slice whose elements the facade can replace with shallow
	// localized display views; the PostgreSQL adapter supplies a new
	// ordered result slice.
	ListPlansForSale(context.Context) ([]*SubscriptionPlan, error)
	// CreatePlan persists the normalized creation request and returns the
	// recorded plan. The PostgreSQL adapter prepares localization and
	// writes the plan and group mappings in a transaction; validation
	// errors from that storage preparation are returned to the caller.
	CreatePlan(context.Context, CreatePlanRequest) (*SubscriptionPlan, error)
	// UpdatePlan applies requested pointer and presence-aware replacements
	// to a plan. The PostgreSQL adapter handles group-rate normalization
	// and localized-content revision checks, preserving omitted fields
	// and returning the updated recorded value or its persistence error.
	UpdatePlan(context.Context, int64, UpdatePlanRequest) (*SubscriptionPlan, error)
	// DeletePlan removes a plan from storage after the facade's separate
	// order-count check. The PostgreSQL adapter performs the entity delete;
	// this interface call does not receive or retain a payment-order
	// reservation from that earlier check.
	DeletePlan(context.Context, int64) error
	// GetPlan returns the requested stored plan for service callers.
	// The PostgreSQL adapter maps read errors to PLAN_NOT_FOUND; callers
	// that need language-specific display apply LocalizePlan to the
	// returned plan value.
	GetPlan(context.Context, int64) (*SubscriptionPlan, error)
}

// PlanOrders supplies the payment-owned count used to guard configured plan deletion.
type PlanOrders interface {
	// CountInProgressByPlan counts qualifying orders associated with
	// the plan ID. Payment defines the included states; Plans.DeletePlan
	// blocks a positive count and propagates count failures before
	// requesting the independent repository delete.
	CountInProgressByPlan(context.Context, int64) (int, error)
}

// Plans prepares sale-term changes and delegates reads and persistence to its adapters.
type Plans struct {
	store PlanRepository // plan persistence adapter
	// orders supplies payment counts when DeletePlan is called. The
	// constructor retains the supplied adapter as provided, so callers
	// bind a usable count source before requesting deletion.
	orders PlanOrders
	// groups confirms that a group selection names stored groups before it
	// is written. A plan write that declares groups fails when this adapter
	// is absent, because the service cannot confirm the selection.
	groups PlanGroupValidator
}

// NewPlans constructs plan operations using the supplied storage,
// order-count, and group-selection adapters. Bind usable storage before
// repository calls, a usable count adapter before DeletePlan, and a usable
// group adapter before writing a plan that declares group identifiers; the
// constructor retains all three dependencies as supplied.
func NewPlans(store PlanRepository, orders PlanOrders, groups PlanGroupValidator) *Plans {
	return &Plans{store: store, orders: orders, groups: groups}
}

// validateGroupIDs confirms that a nonempty group selection names stored groups.
// An empty selection passes without a lookup and leaves the plan unrestricted.
// Missing adapter, lookup failure, and missing group results all stop the plan
// write, keeping a selection that points at a deleted group out of storage. An
// absent adapter reports a server-side failure, because serving plans without
// one would accept selections the deployment cannot confirm.
func (s *Plans) validateGroupIDs(ctx context.Context, groupIDs []int64) error {
	if len(groupIDs) == 0 {
		return nil
	}
	if s.groups == nil {
		return apperror.InternalServer("PLAN_GROUP_VALIDATOR_UNAVAILABLE", "plan group validation is unavailable")
	}
	return s.groups.ValidatePlanGroupIDs(ctx, groupIDs)
}

// ListPlans returns the configured repository's plan list unchanged.
// Storage owns ordering, record construction, and errors; this method
// does not resolve display language, so administrator readers retain
// the recorded plan content.
func (s *Plans) ListPlans(ctx context.Context) ([]*SubscriptionPlan, error) {
	return s.store.ListPlans(ctx)
}

// ListPlansForSale returns offered plans localized for the context
// language. It replaces pointers in the repository's returned slice
// with LocalizePlan views; their nested references remain shared
// with the stored values, and query errors return before localization.
func (s *Plans) ListPlansForSale(ctx context.Context) ([]*SubscriptionPlan, error) {
	items, err := s.store.ListPlansForSale(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = LocalizePlan(items[i], locale.FromContext(ctx))
	}
	return items, nil
}

// GetPlan returns the requested repository plan and its error as
// supplied by storage. Callers establish access to the ID and apply
// display localization where required; this facade adds no ownership
// check or language resolution.
func (s *Plans) GetPlan(ctx context.Context, id int64) (*SubscriptionPlan, error) {
	return s.store.GetPlan(ctx, id)
}

// CreatePlan prepares a normalized sale template before repository
// creation. Supplied localization source overrides the plain name,
// description, feature text, and product name before required-field
// validation. The repository later prepares localized content and
// validates its revision and translated values.
//
// Legacy GroupID is merged into the positive distinct GroupIDs list,
// and only selected group rates survive normalization. The selection
// must name stored groups, so a deleted group cannot enter a new plan.
// Required terms, currency syntax, and quota signs are checked before
// storage. The request passed onward clears the legacy group field and
// contains the same normalized selection used for its rate validation.
func (s *Plans) CreatePlan(ctx context.Context, req CreatePlanRequest) (*SubscriptionPlan, error) {
	if req.Localization != nil {
		copy := req.Localization.Source
		req.Name, req.Description, req.Features, req.ProductName = copy.Name, copy.Description, copy.Features, copy.ProductName
	}
	groupIDs := NormalizePlanGroupIDs(req.GroupID, req.GroupIDs)
	groupRates, err := NormalizePlanGroupRateMultipliers(groupIDs, req.GroupRateMultipliers)
	if err != nil {
		return nil, err
	}
	if err := ValidatePlanRequired(req.Name, req.Price, req.ValidityDays, req.ValidityUnit, req.OriginalPrice); err != nil {
		return nil, err
	}
	currency, err := NormalizePlanCurrency(req.Currency)
	if err != nil {
		return nil, err
	}
	if err := ValidatePlanQuotas(req.DailyLimitUSD, req.WeeklyLimitUSD, req.MonthlyLimitUSD); err != nil {
		return nil, err
	}
	// The group lookup runs after local term checks so a rejected price or quota does not
	// reach storage adapters.
	if err := s.validateGroupIDs(ctx, groupIDs); err != nil {
		return nil, err
	}

	// Storage receives one normalized group selection and its validated
	// rate entries. Clear the legacy leading group in that outgoing
	// request so it describes the same selection as the expanded list.
	req.GroupID = 0
	req.GroupIDs = groupIDs
	req.GroupRateMultipliers = groupRates
	req.Currency = currency
	return s.store.CreatePlan(ctx, req)
}

// UpdatePlan prepares selected plan replacements and delegates their
// persistence. Plain text pointers require a Localization update;
// when that update exists, its source supplies all four plain text
// fields before patch validation.
//
// Ordinary omitted pointers and the nullable fields' presence bits
// remain available for the repository to interpret. A submitted group
// selection must name stored groups before it replaces the stored one;
// an update that leaves group_ids out keeps the recorded selection and
// needs no group lookup. This service checks requested required-field and
// quota comparisons, while the repository normalizes currency and group
// changes and compares localized content revisions. Errors from either
// phase are returned in the phase where they occur.
func (s *Plans) UpdatePlan(ctx context.Context, id int64, req UpdatePlanRequest) (*SubscriptionPlan, error) {
	// Text replacement needs the versioned content update so repository
	// revision checks and translation handling use the submitted source
	// that also supplies the plain compatibility fields.
	if req.Localization == nil && (req.Name != nil || req.Description != nil || req.Features != nil || req.ProductName != nil) {
		return nil, apperror.BadRequest("LOCALIZATION_REQUIRED", "Use localization to update plan text.")
	}
	if req.Localization != nil {
		copy := req.Localization.Source
		req.Name, req.Description, req.Features, req.ProductName = &copy.Name, &copy.Description, &copy.Features, &copy.ProductName
	}
	if err := ValidatePlanPatch(req); err != nil {
		return nil, err
	}
	if req.GroupIDs != nil {
		// Storage merges a supplied legacy identifier ahead of the list, so the check
		// covers the selection that will actually be stored.
		groupID := int64(0)
		if req.GroupID != nil {
			groupID = *req.GroupID
		}
		if err := s.validateGroupIDs(ctx, NormalizePlanGroupIDs(groupID, *req.GroupIDs)); err != nil {
			return nil, err
		}
	}
	return s.store.UpdatePlan(ctx, id, req)
}

// DeletePlan checks payment's qualifying order count before requesting
// plan deletion. Count failures are wrapped, and a positive count
// returns PENDING_ORDERS. The count and repository delete are separate
// calls here, with no plan lock or transaction spanning them.
func (s *Plans) DeletePlan(ctx context.Context, id int64) error {
	count, err := s.orders.CountInProgressByPlan(ctx, id)
	if err != nil {
		return fmt.Errorf("check pending orders: %w", err)
	}
	if count > 0 {
		return apperror.Conflict("PENDING_ORDERS", fmt.Sprintf("this plan has %d in-progress orders and cannot be deleted — wait for orders to complete first", count))
	}
	return s.store.DeletePlan(ctx, id)
}
