package postgres

import (
	"context"
	"database/sql"
	"strings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
)

// plans.go persists subscription plans and synchronizes their relational group
// mappings.
// Plan group selections are stored in the plan's JSON fields and in a
// relational group mapping used by billing queries. Creation saves both within
// an owned Ent transaction. Updates start an owned transaction when groups,
// group rates or localization are supplied; ordinary scalar edits use the
// configured client directly.
//
// Group edits combine requested fields with an existing row before rebuilding
// mappings. That initial group read has no row lock, while localization takes
// a later locked read for content preparation, so transaction ownership alone
// does not serialize every partial group decision. Nullable update fields
// retain omission versus clearing, and lookup paths can collapse database read
// errors into the plan-not-found response.

// PlanStore persists plan records and their mirrored group mappings through
// Ent.
type PlanStore struct {
	// entClient supplies plan reads and starts owned create/group/localization
	// transactions. GetPlan joins a context transaction; other methods use this
	// client directly rather than a transaction carried in context.
	entClient *dbent.Client // configured plan persistence client
}

// planGroupMappingExecutor supplies parameterized SQL writes for a plan's
// mirrored group selection.
type planGroupMappingExecutor interface {
	// ExecContext executes a mapping delete or upsert and returns its database
	// result or error. The caller supplies a transaction-bound executor when the
	// mapping rebuild must commit together with its plan row.
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// NewPlanStore retains an initialized Ent client for plan persistence without
// executing queries. Callers keep the client usable for later methods; owned
// transactions begin only when a create or selected multi-field update runs.
func NewPlanStore(client *dbent.Client) *PlanStore { return &PlanStore{entClient: client} }

// ListPlans returns converted plan records ordered by stored sort order
// ascending. Query errors propagate and empty results produce an allocated
// slice; equal sort values have no additional ordering specified here.
func (s *PlanStore) ListPlans(ctx context.Context) ([]*billing.SubscriptionPlan, error) {
	models, err := s.entClient.SubscriptionPlan.Query().Order(subscriptionplan.BySortOrder()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*billing.SubscriptionPlan, len(models))
	for i, m := range models {
		out[i] = PlanFromEntity(m)
	}
	return out, nil
}

// ListPlansForSale returns converted plans whose stored ForSale flag is true,
// ordered by sort order ascending. It applies that flag alone, leaving sale
// terms and display localization to billing.Plans.
func (s *PlanStore) ListPlansForSale(ctx context.Context) ([]*billing.SubscriptionPlan, error) {
	models, err := s.entClient.SubscriptionPlan.Query().Where(subscriptionplan.ForSaleEQ(true)).Order(subscriptionplan.BySortOrder()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*billing.SubscriptionPlan, len(models))
	for i, m := range models {
		out[i] = PlanFromEntity(m)
	}
	return out, nil
}

// CreatePlan persists a caller-normalized plan and its mirrored group mapping
// inside an owned Ent transaction. It trims name/unit, prepares optional
// localization and maps supplied optional amounts into the new row. The
// billing.Plans caller owns request policy and normalized group/currency
// inputs.
//
// The plan row is saved before mapping deletion/insertion, and both commit
// only after mapping synchronization succeeds. Errors return before success
// with deferred rollback attempted; rollback errors do not replace the primary
// outcome. The method starts from its configured client and does not
// automatically reuse context's Ent transaction, then converts the saved row
// after successful commit.
func (s *PlanStore) CreatePlan(ctx context.Context, req billing.CreatePlanRequest) (*billing.SubscriptionPlan, error) {
	groupIDs, groupRates, currency := req.GroupIDs, req.GroupRateMultipliers, req.Currency

	// Own the plan row and its relational group mirror in one transaction, so a
	// mapping error can discard the earlier saved plan.
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	// Attempt rollback on every unfinished exit; cleanup errors stay secondary to
	// save/mapping/commit results, and successful commit has already ended
	// transaction ownership.
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()

	builder := client.SubscriptionPlan.Create().
		SetName(strings.TrimSpace(req.Name)).
		SetDescription(req.Description).
		SetPrice(req.Price).
		SetCurrency(currency).
		SetValidityDays(req.ValidityDays).
		SetValidityUnit(strings.TrimSpace(req.ValidityUnit)).
		SetGroupIds(groupIDs).
		SetGroupRateMultipliers(groupRates).
		SetFeatures(req.Features).
		SetProductName(req.ProductName).
		SetForSale(req.ForSale).
		SetSortOrder(req.SortOrder)
	if req.OriginalPrice != nil {
		builder.SetOriginalPrice(*req.OriginalPrice)
	}
	if req.DailyLimitUSD != nil {
		builder.SetDailyLimitUsd(*req.DailyLimitUSD)
	}
	if req.WeeklyLimitUSD != nil {
		builder.SetWeeklyLimitUsd(*req.WeeklyLimitUSD)
	}
	if req.MonthlyLimitUSD != nil {
		builder.SetMonthlyLimitUsd(*req.MonthlyLimitUSD)
	}
	if req.Localization != nil {
		next, err := locale.Prepare(locale.Original(billing.PlanCopy{}), *req.Localization, billing.ValidatePlanCopy)
		if err != nil {
			return nil, err
		}
		builder.SetLocalization(billing.PlanLocalization(next))
	}
	plan, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	// Write the relational mirror after the plan exists and before commit; failed
	// group insertion must roll back the earlier plan row too.
	if err := syncPlanGroupMappings(ctx, client, plan.ID, groupIDs, groupRates); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return PlanFromEntity(plan), nil
}

// UpdatePlan applies supplied plan fields and returns the converted saved row.
// Group/rate or localization input starts an owned transaction; scalar-only
// updates use the configured client directly. GroupIDs uses the legacy GroupID
// only when its own pointer is supplied, and absent group/rate input retains
// the corresponding existing data for reconciliation.
//
// Group reconciliation first reads the row without FOR UPDATE and normalizes
// rates against the selected groups. Localization takes a separate later row
// lock and prepares content from that locked view. The group read can
// therefore precede waiting on another writer; this method provides
// transaction grouping rather than a universal serialization guarantee for
// partial group requests.
//
// Present nullable amounts clear on nil and replace on a value; omitted
// amounts stay unchanged. After saving the plan, group changes rebuild
// relational mappings before commit. Any mapping/preparation/write failure
// triggers owned rollback cleanup, while successful scalar-only writes need no
// mapping rebuild.
func (s *PlanStore) UpdatePlan(ctx context.Context, id int64, req billing.UpdatePlanRequest) (*billing.SubscriptionPlan, error) {
	var groupIDs []int64
	var groupRates map[int64]float64
	if req.GroupIDs != nil {
		groupID := int64(0)
		if req.GroupID != nil {
			groupID = *req.GroupID
		}
		groupIDs = billing.NormalizePlanGroupIDs(groupID, *req.GroupIDs)
	}
	// Group mirrors and versioned localization need multiple coordinated
	// operations. Begin an owned transaction for those requests while scalar-only
	// edits keep their single-row path.
	useTx := req.GroupIDs != nil || req.GroupRateMultipliers != nil || req.Localization != nil
	client := s.entClient
	var tx *dbent.Tx
	if useTx {
		var err error
		tx, err = s.entClient.Tx(ctx)
		if err != nil {
			return nil, err
		}
		// This update owns its transaction. Deferred rollback covers preparation,
		// plan or mapping failure while leaving the returned primary error and
		// successful commit result unchanged.
		defer func() { _ = tx.Rollback() }()
		client = tx.Client()
	}

	if req.GroupIDs != nil || req.GroupRateMultipliers != nil {
		existing, err := client.SubscriptionPlan.Get(ctx, id)
		if err != nil {
			// The reconciliation read collapses every repository failure to
			// plan-not-found, including storage errors; callers cannot infer the
			// underlying cause from this response.
			return nil, apperror.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
		}
		// A rate-only edit uses the existing group selection; copy its IDs for
		// normalization and mirror rebuilding while retaining the original
		// requested-field presence.
		if req.GroupIDs == nil {
			groupIDs = append([]int64(nil), existing.GroupIds...)
		}
		rates := existing.GroupRateMultipliers
		if req.GroupRateMultipliers != nil {
			rates = *req.GroupRateMultipliers
		}
		groupRates, err = billing.NormalizePlanGroupRateMultipliers(groupIDs, rates)
		if err != nil {
			return nil, err
		}
	}

	update := client.SubscriptionPlan.UpdateOneID(id)
	if req.Localization != nil {
		// Prepare localization from a locked current content row, holding that lock
		// through the saved replacement and owned commit so this content observation
		// stays protected.
		current, err := client.SubscriptionPlan.Query().Where(subscriptionplan.IDEQ(id)).ForUpdate().Only(ctx)
		if err != nil {
			return nil, err
		}
		content := billing.PlanContent(PlanFromEntity(current))
		next, err := locale.Prepare(content, *req.Localization, billing.ValidatePlanCopy)
		if err != nil {
			return nil, err
		}
		update.SetLocalization(billing.PlanLocalization(next))
	}
	if req.GroupIDs != nil {
		update.SetGroupIds(groupIDs)
	}
	if req.GroupIDs != nil || req.GroupRateMultipliers != nil {
		update.SetGroupRateMultipliers(groupRates)
	}
	if req.Name != nil {
		update.SetName(strings.TrimSpace(*req.Name))
	}
	if req.Description != nil {
		update.SetDescription(*req.Description)
	}
	if req.Price != nil {
		update.SetPrice(*req.Price)
	}
	// The presence bit distinguishes omitted comparison pricing from a requested
	// clear; only present input can remove the stored optional amount.
	if req.OriginalPrice.Present {
		if req.OriginalPrice.Value == nil {
			update.ClearOriginalPrice()
		} else {
			update.SetOriginalPrice(*req.OriginalPrice.Value)
		}
	}
	if req.Currency != nil {
		currency, err := billing.NormalizePlanCurrency(*req.Currency)
		if err != nil {
			return nil, err
		}
		update.SetCurrency(currency)
	}
	if req.ValidityDays != nil {
		update.SetValidityDays(*req.ValidityDays)
	}
	if req.ValidityUnit != nil {
		update.SetValidityUnit(strings.TrimSpace(*req.ValidityUnit))
	}
	// DailyLimitUSD, WeeklyLimitUSD and MonthlyLimitUSD each carry presence beside
	// their nullable value; clear only a requested limit and retain omitted windows.
	if req.DailyLimitUSD.Present {
		if req.DailyLimitUSD.Value == nil {
			update.ClearDailyLimitUsd()
		} else {
			update.SetDailyLimitUsd(*req.DailyLimitUSD.Value)
		}
	}
	if req.WeeklyLimitUSD.Present {
		if req.WeeklyLimitUSD.Value == nil {
			update.ClearWeeklyLimitUsd()
		} else {
			update.SetWeeklyLimitUsd(*req.WeeklyLimitUSD.Value)
		}
	}
	if req.MonthlyLimitUSD.Present {
		if req.MonthlyLimitUSD.Value == nil {
			update.ClearMonthlyLimitUsd()
		} else {
			update.SetMonthlyLimitUsd(*req.MonthlyLimitUSD.Value)
		}
	}
	if req.Features != nil {
		update.SetFeatures(*req.Features)
	}
	if req.ProductName != nil {
		update.SetProductName(*req.ProductName)
	}
	if req.ForSale != nil {
		update.SetForSale(*req.ForSale)
	}
	if req.SortOrder != nil {
		update.SetSortOrder(*req.SortOrder)
	}
	plan, err := update.Save(ctx)
	if err != nil {
		return nil, err
	}
	if req.GroupIDs != nil || req.GroupRateMultipliers != nil {
		// Rebuild mapping rows before the owned commit so this request publishes its
		// plan group/rate changes together with its relational selection.
		if err := syncPlanGroupMappings(ctx, client, id, groupIDs, groupRates); err != nil {
			return nil, err
		}
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return PlanFromEntity(plan), nil
}

// syncPlanGroupMappings rebuilds one plan's relational group selection using
// the supplied executor. Nil executor or nonpositive plan ID is a no-op;
// callers needing an atomic plan/mapping update supply a transaction-bound
// client and valid plan ID. Existing mappings are deleted before requested
// groups are inserted.
//
// Nonpositive group IDs are skipped, and repeated positive IDs use upsert
// conflict handling. A missing or nonpositive selected rate becomes SQL NULL,
// leaving that group unconfigured for an override. Errors stop the sequence
// and rely on the caller's transaction rollback; this helper starts no
// transaction or commit of its own.
func syncPlanGroupMappings(ctx context.Context, exec planGroupMappingExecutor, planID int64, groupIDs []int64, rates map[int64]float64) error {
	if exec == nil || planID <= 0 {
		return nil
	}
	// Delete the prior mirror before inserting selected groups. A
	// transaction-bound caller can roll this entire rebuild back if any later
	// upsert fails.
	if _, err := exec.ExecContext(ctx, `DELETE FROM subscription_plan_groups WHERE plan_id = $1`, planID); err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		// Unconfigured rates become SQL NULL for selected groups, allowing
		// downstream rate selection to use its command default for those groups.
		var rate any
		if value, ok := rates[groupID]; ok && value > 0 {
			rate = value
		}
		if _, err := exec.ExecContext(ctx, `
			INSERT INTO subscription_plan_groups (plan_id, group_id, rate_multiplier)
			VALUES ($1, $2, $3)
			ON CONFLICT (plan_id, group_id)
			DO UPDATE SET rate_multiplier = EXCLUDED.rate_multiplier
		`, planID, groupID, rate); err != nil {
			return err
		}
	}
	return nil
}

// DeletePlan delegates removal of the selected plan to the configured Ent
// client and returns its execution error. It starts no explicit transaction
// here; database relationships determine effects on linked mapping records.
func (s *PlanStore) DeletePlan(ctx context.Context, id int64) error {
	return s.entClient.SubscriptionPlan.DeleteOneID(id).Exec(ctx)
}

// GetPlan returns the selected record converted to a billing plan. Every Ent
// read error is mapped to PLAN_NOT_FOUND on this path, including database
// failures, so callers receive that shared absence response. A context Ent
// transaction selects its connection, preserving read-your-writes and avoiding
// another pool checkout while a caller already owns a transaction.
func (s *PlanStore) GetPlan(ctx context.Context, id int64) (*billing.SubscriptionPlan, error) {
	plan, err := clientFromContext(ctx, s.entClient).SubscriptionPlan.Get(ctx, id)
	// This lookup converts every read failure into the public plan-not-found
	// error, so the underlying database classification is absent from this
	// result.
	if err != nil {
		return nil, apperror.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
	}
	return PlanFromEntity(plan), nil
}
