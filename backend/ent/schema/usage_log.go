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

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// usage_log.go defines completed request records containing execution, usage,
// and funding facts for statistics and audits. User-side funding amounts can
// differ from the provider pricing base, and separate request and billing
// identifiers keep query correlation distinct from financial deduplication.

// UsageLog declares historical request attribution, measured usage, and calculated billing facts.
type UsageLog struct {
	ent.Schema // generated entity definition
}

// Annotations maps completed request facts to the usage_logs database table.
func (UsageLog) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "usage_logs"},
	}
}

// Fields declares attribution, token and media dimensions, costs, and
// request metadata. Generated inserts default usage and costs to zero;
// current SQL writers also populate migrated columns beyond this Ent
// schema and preserve supplied historical attribution.
func (UsageLog) Fields() []ent.Field {
	return []ent.Field{
		// Actual frame billing units for animated output; zero denotes static output.
		field.Int("image_frame_count").Default(0),
		// Explicit cached image input; absent upstream subdivision remains zero.
		field.Int("image_cache_read_tokens").Default(0),
		// Recorded customer or internal execution source.
		field.String("request_source").MaxLen(32).Default("user"),
		// Internal execution attribution and measurement completeness.
		field.JSON("system_usage", map[string]any{}).Optional(),
		// user_id, billing_user_id, team_id, and api_key_id separate the acting
		// user, recorded payer, optional team scope, and credential. Historical
		// payer writes are supported by migration 221, while current repositories
		// write their resolved payer snapshot.
		// Acting request user.
		field.Int64("user_id").Optional().Nillable(),
		// Recorded request payer.
		field.Int64("billing_user_id").Optional().Nillable(),
		// Optional request team scope.
		field.Int64("team_id").Optional().Nillable(),
		// Authenticated request key.
		field.Int64("api_key_id").Optional().Nillable(),
		// Selected execution provider.
		field.Int64("provider_id"),
		// platform snapshots the execution platform so later provider changes
		// do not rewrite historical attribution. The generated default is unknown;
		// repository insertion normalizes supplied reporting platform values.
		field.String("platform").MaxLen(64).Default("unknown"),
		// billing_key identifies the financial operation independently of the
		// request identifier. Migration 293 deduplicates COALESCE(billing_key,
		// request_id) with api_key_id, allowing older rows to retain their
		// request-based financial identity.
		field.String("billing_key").MaxLen(255).Optional().Nillable(),
		// Local request correlation identifier.
		field.String("request_id").
			MaxLen(64).
			NotEmpty(),
		// Recorded request model.
		field.String("model").
			MaxLen(100).
			NotEmpty(),
		// requested_model retains the client model for display and analytics.
		// Current usage insertion substitutes model when this input is empty;
		// historical rows can remain null and use display-time compatibility rules.
		field.String("requested_model").
			MaxLen(100).
			Optional().
			Nillable(),
		// upstream_model retains the model sent to the provider when recorded.
		// An absent value means that no upstream model was stored for the row;
		// model mapping and response-model observations remain separate facts.
		field.String("upstream_model").
			MaxLen(100).
			Optional().
			Nillable(),
		// upstream_response_model and upstream_model_mismatch retain the directly
		// observed response declaration and its comparison result for administrator
		// audits. Missing observations and legacy rows can leave both values
		// null; these fields do not select the billing price.
		// Observed response model.
		field.String("upstream_response_model").MaxLen(200).Optional().Nillable(),
		// Observed model-comparison result.
		field.Bool("upstream_model_mismatch").Optional().Nillable(),
		// Resolved shared pricing configuration.
		field.Int64("pricing_config_id").Optional().Nillable().Comment("Shared pricing configuration ID"),
		// Recorded model transformation chain.
		field.String("model_mapping_chain").MaxLen(500).Optional().Nillable().Comment("Model mapping chain"),
		// Selected price-tier label.
		field.String("billing_tier").MaxLen(50).Optional().Nillable().Comment("Billing tier label"),
		// Selected pricing calculation mode.
		field.String("billing_mode").MaxLen(20).Optional().Nillable().Comment("Billing mode: token/per_request/image"),
		// Optional final request group.
		field.Int64("group_id").
			Optional().
			Nillable(),
		// Optional recorded funding subscription.
		field.Int64("subscription_id").
			Optional().
			Nillable(),

		// input_tokens, output_tokens, cache_creation_tokens, and cache_read_tokens
		// retain the aggregate token dimensions. cache_creation_5m_tokens and
		// cache_creation_1h_tokens separately retain TTL detail; UsageLog.TotalTokens
		// adds the aggregate dimensions, so those TTL fields are not added again.
		// Uncached input token count.
		field.Int("input_tokens").
			Default(0),
		// Generated output token count.
		field.Int("output_tokens").
			Default(0),
		// Aggregate cache-creation token count.
		field.Int("cache_creation_tokens").
			Default(0),
		// Reused cache token count.
		field.Int("cache_read_tokens").
			Default(0),
		// Five-minute cache-creation token count.
		field.Int("cache_creation_5m_tokens").
			Default(0),
		// One-hour cache-creation token count.
		field.Int("cache_creation_1h_tokens").
			Default(0),

		// USD input-token cost.
		field.Float("input_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// USD output-token cost.
		field.Float("output_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// USD cache-creation cost.
		field.Float("cache_creation_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// USD cache-read cost.
		field.Float("cache_read_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// total_cost retains the pricing cost before funding multipliers, while
		// actual_cost records the user-side amount. Free-OpenAI-Fast policy can
		// reprice user funding at Standard while retaining the upstream Fast
		// base for provider statistics.
		// USD pricing-base total.
		field.Float("total_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// USD user-side total.
		field.Float("actual_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// subscription_amount_usd, balance_amount_usd, and billing_allocations
		// retain funding outcomes by source. Individual allocations can identify
		// several subscriptions and source-specific multipliers; the single
		// subscription_id remains the row-level attribution value.
		// USD subscription-funded amount.
		field.Float("subscription_amount_usd").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// USD balance-funded amount.
		field.Float("balance_amount_usd").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// Recorded per-source funding allocations.
		field.JSON("billing_allocations", []billing.BillingAllocation{}).
			Optional().
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		// rate_multiplier records the selected user pricing multiplier. When
		// settlement returns an effective multiplier for positive base cost,
		// ApplyResultToLog replaces it with the total funded amount divided by
		// that base, reflecting mixed subscription and balance funding.
		field.Float("rate_multiplier").
			Default(1).
			SchemaType(map[string]string{dialect.Postgres: "decimal(10,4)"}),
		// long_context_billing_applied records whether the resolved long-context
		// rules increased this request's cost. Merely crossing a configured context
		// threshold does not establish an increase when the resulting price is free.
		field.Bool("long_context_billing_applied").
			Default(false).
			Comment("Records whether long-context rules increased this request's cost"),

		// provider_rate_multiplier snapshots the provider cost multiplier. Usage
		// statistics treat historical null values as one, independently of the
		// user-side amounts recorded in the funding fields.
		field.Float("provider_rate_multiplier").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(10,4)"}),

		// Legacy funding category: zero for balance, one for subscription.
		field.Int8("billing_type").
			Default(0),
		// Recorded streaming-response flag.
		field.Bool("stream").
			Default(false),
		// Elapsed request duration, milliseconds.
		field.Int("duration_ms").
			Optional().
			Nillable(),
		// Timings stores the immutable performance observations owned by this usage row.
		field.JSON("timings", map[string]any{}).Optional(),
		// Recorded client user agent.
		field.String("user_agent").
			MaxLen(512).
			Optional().
			Nillable(),
		// Recorded client IP address.
		field.String("ip_address").
			MaxLen(45). // Accommodate IPv6 text that contains embedded IPv4 octets.
			Optional().
			Nillable(),

		// Generated image output count.
		field.Int("image_count").
			Default(0),
		// Selected image billing size.
		field.String("image_size").
			MaxLen(10).
			Optional().
			Nillable(),
		// Recorded input image size.
		field.String("image_input_size").
			MaxLen(32).
			Optional().
			Nillable(),
		// Recorded output image size.
		field.String("image_output_size").
			MaxLen(32).
			Optional().
			Nillable(),
		// Recorded billing-size selection source.
		field.String("image_size_source").
			MaxLen(16).
			Optional().
			Nillable(),
		// Image counts by billing size.
		field.JSON("image_size_breakdown", map[string]int{}).
			Optional().
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),

		// video_count identifies generated video usage when positive. Analytics
		// can recognize such rows independently of billing_mode, while Grok
		// video pricing uses the separately recorded duration in seconds.
		field.Int("video_count").
			Default(0).
			Comment("Generated video count; a positive value identifies video usage"),
		// The normalized video label selects a configured per-second price. Custom
		// labels share the fifty-character limit of pricing interval labels.
		field.String("video_resolution").
			MaxRuneLen(50).
			Optional().
			Nillable().
			Comment("Normalized video resolution label used for per-second billing"),
		// video_duration_seconds records the duration selected for video billing.
		// Grok completion prefers a positive duration from the status response,
		// then the creation snapshot, and uses the pricing default when both
		// sources omit a usable duration.
		field.Int("video_duration_seconds").
			Optional().
			Nillable().
			Comment("Billable video seconds from completion, submission, or the missing-duration default"),
		// Administrator cache-TTL billing override.
		field.Bool("cache_ttl_overridden").
			Default(false),

		// created_at is immutable and defaults to time.Now for generated Ent
		// inserts. The raw SQL usage repository can retain a supplied request
		// time; this declaration supplies no updated_at field.
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

// Edges links request records to users, credentials, providers, and optional funding scope.
func (UsageLog) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("usage_logs").
			Field("user_id").
			Unique(),
		edge.From("api_key", APIKey.Type).
			Ref("usage_logs").
			Field("api_key_id").
			Unique(),
		edge.From("provider", Provider.Type).
			Ref("usage_logs").
			Field("provider_id").
			Required().
			Unique(),
		edge.From("group", Group.Type).
			Ref("usage_logs").
			Field("group_id").
			Unique(),
		edge.From("subscription", UserSubscription.Type).
			Ref("usage_logs").
			Field("subscription_id").
			Unique(),
		edge.From("team", Team.Type).
			Ref("usage_logs").
			Field("team_id").
			Unique(),
	}
}

// Indexes declares attribution, model, request, and time-range lookups
// for Ent. Production migrations additionally define partial group
// indexes and billing-key deduplication, so callers use the deployed
// migration schema when evaluating uniqueness.
func (UsageLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id"),
		index.Fields("billing_user_id"),
		index.Fields("team_id"),
		index.Fields("api_key_id"),
		index.Fields("provider_id"),
		index.Fields("group_id"),
		index.Fields("subscription_id"),
		index.Fields("created_at"),
		index.Fields("model"),
		index.Fields("requested_model"),
		index.Fields("request_id"),
		index.Fields("user_id", "created_at"),
		index.Fields("api_key_id", "created_at"),
		index.Fields("group_id", "created_at"),
	}
}
