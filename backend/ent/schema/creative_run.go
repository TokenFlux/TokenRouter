package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// creative_run.go defines creative-run identity, funding, and recovery
// metadata, with prompt and image payloads held separately in temporary Redis
// storage. Recorded provisioning steps and provider outcomes let the creative
// workflow resume unfinished work and distinguish successful generation from
// later result delivery.

// CreativeRun stores asynchronous creative task metadata and its funding snapshot.
type CreativeRun struct {
	ent.Schema // generated entity definition
}

// Annotations maps generated creative-run operations to the creative_runs table.
func (CreativeRun) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "creative_runs"},
	}
}

// Mixin supplies creation and update timestamps for persisted creative runs.
func (CreativeRun) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

// Fields describes run ownership, model selection, output preferences, funding,
// and recovery progress. Generated Ent inserts start queued with one requested
// output and version one. Optional outcomes and timestamps remain unset until
// the creative repository records those phases.
func (CreativeRun) Fields() []ent.Field {
	return []ent.Field{
		// Requested animation count; zero preserves static task pricing.
		field.Int("frame_count").Default(0),
		// Requested frame delay, in milliseconds, for animated tasks only.
		field.Int("frame_duration_ms").Default(0),
		// Immutable public identity.
		field.String("run_id").MaxLen(64).Immutable(),
		// Resource owner identity.
		field.Int64("user_id"),
		// workspace_id partitions the owner's browser histories. Public queries
		// require the requested workspace to match, so legacy NULL values are
		// hidden from those histories and owner-specific output operations.
		field.String("workspace_id").Optional().Nillable().MaxLen(64),
		// Selected routing group.
		field.Int64("group_id"),
		// api_key_id references the workflow's managed creative execution key.
		// Public supplies that key for the selected user and group when creating
		// the run used by execution and funding.
		field.Int64("api_key_id"),
		// provider_id and platform identify the selected execution provider.
		// The repository creates a run with its catalog platform, then execution
		// preparation records the chosen provider ID and platform through
		// SetCreativeRunExecution before the provider call.
		// Selected provider identity.
		field.Int64("provider_id").Optional().Nillable(),
		// Recorded provider platform.
		field.String("platform").MaxLen(32).Default(""),
		// model and requested_model keep the validated catalog choice and client
		// value in the same run. Public supplies both on creation, so request
		// views can present the client's submitted value independently of the
		// model selected during validation.
		// Selected catalog model.
		field.String("model").MaxLen(128),
		// requested_model records the client model used by request views.
		// Run decoding falls back to model when this field is empty, supporting
		// rows created before the client-model snapshot was recorded.
		field.String("requested_model").MaxLen(128).Default(""),
		// Public validates operation as generate for a prompt, edit for source
		// images, or inpaint for a source-and-mask request. The selected operation
		// determines the execution adapter's request shape.
		field.String("operation").MaxLen(16),
		// Requested image count.
		field.Int("requested_output_count").Default(1),
		// Selected resolution tier.
		field.String("image_size").MaxLen(16).Default("1K"),
		// Requested aspect ratio.
		field.String("aspect_ratio").MaxLen(16).Default(""),
		// Stored MIME preference.
		field.String("response_mime_type").MaxLen(64).Default("image/png"),
		// prompt_hash stores the hexadecimal SHA-256 digest computed during
		// request validation. The prompt text used for execution is held in the
		// temporary payload associated with the run.
		field.String("prompt_hash").MaxLen(64),
		// request_fingerprint identifies the canonical request payload by its
		// SHA-256 digest. Public compares the digest when an idempotency key
		// finds an existing run; identical content can also have distinct run keys.
		field.String("request_fingerprint").MaxLen(64),
		// Scoped client retry key.
		field.String("idempotency_key").Optional().Nillable().MaxLen(255),
		// Persisted processing phase.
		field.String("status").MaxLen(20).Default("queued"),
		// estimated_cost, hold_amount, and actual_cost describe expected cost,
		// the reservation, and recorded final cost. The final amount remains
		// optional while provisioning, execution, or settlement has not yet
		// recorded a completed funding outcome.
		// USD expected charge.
		field.Float("estimated_cost").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		// BuildHoldCommand uses hold_amount when present and estimated_cost when
		// absent. The field therefore records the reservation's amount separately
		// from the displayed cost estimate.
		// USD funding reservation.
		field.Float("hold_amount").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// USD recorded cost.
		field.Float("actual_cost").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// balance_hold_amount and subscription_hold_allocations describe the
		// reserved wallet and subscription funds together. Creative funding uses
		// auto mode and submits these recorded allocations during later capture
		// or release of the run's reservation.
		// USD wallet reservation.
		field.Float("balance_hold_amount").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		// Reserved subscription allocations.
		field.JSON("subscription_hold_allocations", []billing.BillingAllocation{}).
			Default(func() []billing.BillingAllocation { return []billing.BillingAllocation{} }).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		// base_unit_price, subscription_rate_multiplier, and balance_rate_multiplier
		// preserve the validated quote's base image price and source-specific
		// factors. BuildHoldCommand constructs base amounts from price and
		// requested output count, then passes the recorded factors to billing.
		// USD per-image base.
		field.Float("base_unit_price").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		// Subscription pricing factor.
		field.Float("subscription_rate_multiplier").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(1),
		// Wallet pricing factor.
		field.Float("balance_rate_multiplier").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(1),
		// plan_group_rate_multiplier_enabled controls the creative funding command's
		// plan-rate choice. False sets DisablePlanGroupRateMultiplier while the
		// recorded base price and source-specific factors remain available.
		field.Bool("plan_group_rate_multiplier_enabled").Default(true),
		// Recorded failure code.
		field.String("error_code").Optional().Nillable().MaxLen(128),
		// Recorded failure description.
		field.String("error_message").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		// release_target_status records the terminal outcome to apply after
		// release_pending succeeds. Release transition setup uses failed when
		// the caller supplies no target status.
		field.String("release_target_status").MaxLen(20).Default("failed"),
		// Execution attempt count.
		field.Int("attempt_count").Default(0),
		// allowance_reserved records the task's allowance reservation. The
		// SettlementStore writes true on reservation and false during successful
		// capture or release participation, through the creative funding participant.
		field.Bool("allowance_reserved").Default(false),
		// provisioning_phase records creation progress for Results recovery.
		// Public advances it after reserving funds, saving transient input, and
		// enqueueing; recovery distinguishes transient_saved from enqueued or complete.
		field.String("provisioning_phase").MaxLen(32).Default("created"),
		// provider_result_recorded_at marks durable provider outcome metadata.
		// RecordProviderOutcome treats an existing value as already recorded,
		// while storing Redis image bytes remains a later delivery step.
		field.Time("provider_result_recorded_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// Settlement attempt count.
		field.Int("settlement_attempt_count").Default(0),
		// Release attempt count.
		field.Int("release_attempt_count").Default(0),
		// next_reconcile_at and last_reconcile_error retain attempt metadata.
		// SetCreativeRunReconcileError clears the time for a zero next value and
		// clears the message for blank input, independently of each other.
		// Recorded next-attempt time.
		field.Time("next_reconcile_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// Latest reconciliation failure.
		field.String("last_reconcile_error").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		// version starts at one and participates in conditional status updates.
		// TransitionCreativeRunStatus, RecordProviderOutcome, and
		// CompleteProviderOutcome compare the loaded version and increment it
		// when their mutation succeeds, rejecting a competing mutation's stale input.
		field.Int64("version").Default(1),
		// Execution start timestamp.
		field.Time("started_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// Recorded completion timestamp.
		field.Time("completed_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// Recorded cancellation timestamp.
		field.Time("cancelled_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

// Indexes supports run identity, owner/workspace history, and queued-or-running
// scans. Nonempty idempotency keys are unique within a user and non-null
// workspace; request fingerprints themselves remain nonunique for repeated content.
func (CreativeRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("run_id").Unique(),
		index.Fields("user_id", "created_at"),
		index.Fields("user_id", "workspace_id", "created_at"),
		index.Fields("status").Annotations(entsql.IndexWhere("status IN ('queued', 'running')")),
		index.Fields("user_id", "workspace_id", "idempotency_key").
			Unique().
			Annotations(entsql.IndexWhere("workspace_id IS NOT NULL AND idempotency_key IS NOT NULL AND idempotency_key <> ''")),
	}
}
