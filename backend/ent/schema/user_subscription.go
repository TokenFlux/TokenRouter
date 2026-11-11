package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// user_subscription.go defines issued subscription entitlements with their own
// duration and quota fields. Each record can begin immediately or wait behind
// another grant, while independent usage windows track consumption and the
// plan relationship supplies group eligibility for later billing checks.

// UserSubscription stores issued entitlement periods, quota limits, and window usage.
type UserSubscription struct {
	ent.Schema // generated entity definition
}

// Annotations maps issued entitlement records to the user_subscriptions database table.
func (UserSubscription) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "user_subscriptions"},
	}
}

// Mixin adds timestamps and default soft-deletion handling to issued subscriptions.
// Ordinary Ent queries exclude deleted records. Billing treats a deleted
// subscription as revoked when deriving its effective lifecycle status.
func (UserSubscription) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

// Fields declares entitlement ownership, period, status, quota windows, and grant
// attribution. Generated Ent inserts start active with zero recorded usage.
// Assignment supplies the start, expiry, and quota snapshot; optional window
// starts distinguish limits whose usage windows have not been activated.
func (UserSubscription) Fields() []ent.Field {
	return []ent.Field{
		// Entitlement owner identity.
		field.Int64("user_id"),
		// Linked product template.
		field.Int64("plan_id"),

		// starts_at and expires_at define the inclusive-start, exclusive-end
		// entitlement period. Billing derives effective status from the current
		// time and deleted_at alongside the stored status value.
		// Inclusive entitlement start.
		field.Time("starts_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// Exclusive entitlement expiry.
		field.Time("expires_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// status retains the stored lifecycle label. For a current, undeleted row,
		// EffectiveStatus keeps suspended and derives active for every other value.
		field.String("status").
			MaxLen(20).
			Default(billing.SubscriptionStatusActive),

		// daily_window_start, weekly_window_start, and monthly_window_start belong
		// to their matching usage totals. Missing starts represent unactivated
		// windows; billing aligns daily resets to the calendar day and uses
		// seven-day and thirty-day periods for weekly and monthly reset checks.
		// Daily usage origin.
		field.Time("daily_window_start").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// Weekly usage origin.
		field.Time("weekly_window_start").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// Monthly usage origin.
		field.Time("monthly_window_start").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),

		// daily_limit_usd, weekly_limit_usd, and monthly_limit_usd are the issued
		// quota snapshot, independently of current plan quota edits. Positive
		// values constrain matching usage windows; absent or nonpositive values
		// impose no limit for their individual period.
		// USD daily quota.
		field.Float("daily_limit_usd").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		// USD weekly quota.
		field.Float("weekly_limit_usd").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		// USD monthly quota.
		field.Float("monthly_limit_usd").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),

		// daily_usage_usd, weekly_usage_usd, and monthly_usage_usd record charges
		// and task reservations in the corresponding windows. Billing updates
		// configured finite periods when reserving or settling funds and resets
		// each counter with its new start under the quota-window rules.
		// USD daily usage.
		field.Float("daily_usage_usd").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).
			Default(0),
		// USD weekly usage.
		field.Float("weekly_usage_usd").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).
			Default(0),
		// USD monthly usage.
		field.Float("monthly_usage_usd").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).
			Default(0),

		// Assigning user identity.
		field.Int64("assigned_by").
			Optional().
			Nillable(),
		// Entitlement assignment timestamp.
		field.Time("assigned_at").
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// source_order_id links payment-created grants to their local order.
		// SubscriptionService looks for an existing grant from that source while
		// serializing assignment for the user, so payment retries can reuse it.
		field.Int64("source_order_id").
			Optional().
			Nillable(),
		// refund_hold_operation_id identifies the refund that temporarily blocks
		// this grant from funding new work. NULL leaves the usual lifecycle and
		// quota rules in effect. Only the owning refund may release its hold.
		field.String("refund_hold_operation_id").
			Optional().
			Nillable(),
		// Optional grant notes.
		field.String("notes").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

// Edges requires an issued entitlement to reference its user and product plan.
// The assigning user is optional, and usage-log traversal exposes request
// records associated with the issued subscription.
func (UserSubscription) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("subscriptions").
			Field("user_id").
			Unique().
			Required(),
		edge.From("plan", SubscriptionPlan.Type).
			Ref("subscriptions").
			Field("plan_id").
			Unique().
			Required(),
		edge.From("assigned_by_user", User.Type).
			Ref("assigned_subscriptions").
			Field("assigned_by").
			Unique(),
		edge.To("usage_logs", UsageLog.Type),
	}
}

// Indexes supports owner/plan histories, effective-period scans, source-order
// lookup, assignment attribution, and retained-row maintenance. Multiple
// subscriptions can form a user's plan chain, with starts_at ordering the
// individual issued periods.
func (UserSubscription) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id"),
		index.Fields("plan_id"),
		index.Fields("status"),
		index.Fields("starts_at"),
		index.Fields("expires_at"),
		index.Fields("source_order_id"),
		index.Fields("user_id", "status", "starts_at", "expires_at"),
		index.Fields("assigned_by"),
		index.Fields("user_id", "plan_id", "starts_at"),
		index.Fields("deleted_at"),
	}
}
