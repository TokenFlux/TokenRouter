package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

// api_key.go defines stored API credentials, ownership, routing choices, and
// admission settings. Personal ownership and optional team scope stay separate
// from the payer resolved for a request, while quotas and spending windows
// retain the key's recorded charges and task reservations.

// APIKey stores access credentials, routing choices, and per-key admission settings.
type APIKey struct {
	ent.Schema // generated entity definition
}

// Annotations maps stored access credentials to the api_keys database table.
func (APIKey) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "api_keys"},
	}
}

// Mixin supplies timestamps and default soft-deletion behavior for access keys.
// Ordinary Ent queries exclude deleted rows. SkipSoftDelete contexts allow
// maintenance to inspect retained keys or remove them physically.
func (APIKey) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

// Fields describes credential ownership, group selection, funding, and limits.
// Generated Ent inserts start active with ordinary group binding, automatic
// funding, client-following Fast policy, and zero quota or spending limits.
// The key service validates combinations before repository writes.
func (APIKey) Fields() []ent.Field {
	return []ent.Field{
		// Linked user identity.
		field.Int64("user_id"),
		// Optional team scope.
		field.Int64("team_id").Optional().Nillable(),
		// team_owner_disabled records the team's owner-imposed key lock.
		// APIKey.IsActive requires it to be false, and ordinary member updates
		// cannot reactivate a key while that owner lock remains set.
		field.Bool("team_owner_disabled").Default(false),
		// Stored access credential.
		field.String("key").
			MaxLen(128).
			NotEmpty().
			Unique(),
		// User-visible key name.
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		// group_id and is_composite describe the binding mode together. Ordinary
		// keys use group_id; composite keys select from APIKeyCompositeGroup
		// prefix mappings, with key-service validation rejecting simultaneous
		// ordinary and composite group bindings.
		// Ordinary bound group.
		field.Int64("group_id").
			Optional().
			Nillable(),
		// Composite binding flag.
		field.Bool("is_composite").
			Default(false).
			Comment("Routes across groups using model prefixes"),
		// Stored activation state.
		field.String("status").
			MaxLen(20).
			Default(apikey.StatusActive),
		// fast_mode_policy records follow_request, force_on, or force_off for
		// this credential. The default follows the incoming client's Fast
		// choice, and the request policy resolver applies the stored override.
		field.String("fast_mode_policy").
			MaxLen(32).
			Default("follow_request").
			Comment("API key Fast mode policy: follow_request, force_on, or force_off"),
		// billing_mode and preferred_subscription_id determine the funding
		// selection made for the key's payer. Auto can use subscriptions then
		// wallet funds; subscription selects the recorded entitlement, and
		// balance selects wallet funding.
		// Configured funding mode.
		field.String("billing_mode").
			MaxLen(32).
			Default("auto").
			Comment("API key billing mode: auto, subscription, or balance"),
		// preferred_subscription_id identifies the entitlement required in
		// subscription mode. Key-service checks apply the payer's subscription
		// ownership and group coverage before accepting that selection.
		field.Int64("preferred_subscription_id").
			Optional().
			Nillable().
			Comment("User subscription ID selected when billing_mode is subscription"),
		// model_mapping redirects client aliases after composite prefix selection
		// and before group/provider mapping. Key redirects match once, with
		// exact rules preferred over matching suffix-wildcard rules.
		field.JSON("model_mapping", map[string]string{}).
			Default(func() map[string]string { return map[string]string{} }).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}).
			Comment("Custom model redirect rules for this API key"),
		// Latest usage timestamp.
		field.Time("last_used_at").
			Optional().
			Nillable().
			Comment("Last usage time of this API key"),
		// ip_whitelist contains address and CIDR rules used by authentication.
		// CheckIPRestrictionWithCompiledRules requires a match when the compiled
		// list records supplied entries, including lists with no valid rules.
		field.JSON("ip_whitelist", []string{}).
			Optional().
			Comment("Allowed IPs/CIDRs, e.g. [\"192.168.1.100\", \"10.0.0.0/8\"]"),
		// ip_blacklist contains address and CIDR rules checked before whitelist
		// admission. A blacklist match rejects the address even when the whitelist
		// also matches; invalid rules are skipped during compilation.
		field.JSON("ip_blacklist", []string{}).
			Optional().
			Comment("Blocked IPs/CIDRs"),

		// quota and quota_used describe the lifetime spending allowance.
		// A zero quota disables its ceiling. Billing task reservations increase
		// quota_used for the hold, and capture or release reconciles that
		// reserved amount with the task outcome.
		// USD lifetime limit.
		field.Float("quota").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Quota limit in USD for this API key (0 = unlimited)"),
		// USD consumed/reserved quota.
		field.Float("quota_used").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Used quota amount in USD"),
		// expires_at limits credential lifetime when set. APIKey.IsExpired
		// compares the timestamp with current time, while an absent value
		// leaves activation and admission rules to their other checks.
		field.Time("expires_at").
			Optional().
			Nillable().
			Comment("Expiration time for this API key (null = never expires)"),

		// max_token_rate_multiplier protects users from increased group Token rates.
		// Zero disables the guard; admission compares positive caps before upstream dispatch.
		field.Float("max_token_rate_multiplier").
			Min(0).
			Default(0),
		// concurrency_limit and rpm_limit are per-key request admission limits.
		// Generated Ent validation rejects negative values, and zero disables
		// each key-specific limit while user and group admission checks remain
		// independently applicable.
		// Concurrent request limit.
		field.Int("concurrency_limit").NonNegative().Default(0),
		// Requests per minute.
		field.Int("rpm_limit").NonNegative().Default(0),

		// rate_limit_5h, rate_limit_1d, and rate_limit_7d constrain spending in
		// their corresponding duration windows. Positive values enable each
		// ceiling; billing checks and records usage against the matching
		// usage counter and window start.
		// USD five-hour limit.
		field.Float("rate_limit_5h").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Rate limit in USD per 5 hours (0 = unlimited)"),
		// USD one-day limit.
		field.Float("rate_limit_1d").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Rate limit in USD per day (0 = unlimited)"),
		// USD seven-day limit.
		field.Float("rate_limit_7d").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Rate limit in USD per 7 days (0 = unlimited)"),
		// usage_5h, usage_1d, and usage_7d retain charges and task allowance
		// reservations for their matching windows. Task billing initializes or
		// resets each counter when its recorded window is absent or elapsed,
		// then adds the reserved amount.
		// USD five-hour usage.
		field.Float("usage_5h").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Used amount in USD for the current 5h window"),
		// USD one-day usage.
		field.Float("usage_1d").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Used amount in USD for the current 1d window"),
		// USD seven-day usage.
		field.Float("usage_7d").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Used amount in USD for the current 7d window"),
		// window_5h_start, window_1d_start, and window_7d_start identify the
		// counter periods. APIKey treats missing or elapsed windows as expired
		// when reading current usage; billing sets a new start with the next
		// recorded charge or reservation.
		// Five-hour window origin.
		field.Time("window_5h_start").
			Optional().
			Nillable().
			Comment("Start time of the current 5h rate limit window"),
		// One-day window origin.
		field.Time("window_1d_start").
			Optional().
			Nillable().
			Comment("Start time of the current 1d rate limit window"),
		// Seven-day window origin.
		field.Time("window_7d_start").
			Optional().
			Nillable().
			Comment("Start time of the current 7d rate limit window"),

		// fallback_when_group_unavailable permits the administrator-configured
		// unavailable-group target. The key flag grants permission for that
		// request fallback; configuration and final-group eligibility determine
		// whether a usable target exists.
		field.Bool("fallback_when_group_unavailable").
			Default(true).
			Comment("Falls back to the same platform's default group when the bound group is unavailable"),
		// managed_by identifies server-managed credentials such as creative_studio
		// execution keys. Ordinary user key flows exclude or reject managed
		// records, while their owning workflow retains access for task execution.
		field.String("managed_by").
			MaxLen(32).
			Optional().
			Nillable().
			Comment("Managing source: creative_studio for hidden execution keys; NULL for ordinary user keys"),
	}
}

// Edges requires a linked user and optionally relates a key to group or team.
// Physical key deletion cascades to composite mappings, and usage-log
// traversal exposes request records associated with the credential.
func (APIKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("api_keys").
			Field("user_id").
			Unique().
			Required(),
		edge.From("group", Group.Type).
			Ref("api_keys").
			Field("group_id").
			Unique(),
		edge.To("usage_logs", UsageLog.Type),
		edge.To("composite_groups", APIKeyCompositeGroup.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("team", Team.Type).
			Ref("api_keys").
			Field("team_id").
			Unique(),
	}
}

// Indexes supports user/team/group lists, activation and retained-row filtering,
// usage chronology, and quota or expiry lookup. The key field declares
// credential uniqueness separately from these additional indexes.
func (APIKey) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id"),
		index.Fields("team_id"),
		index.Fields("group_id"),
		index.Fields("status"),
		index.Fields("deleted_at"),
		index.Fields("last_used_at"),
		index.Fields("quota", "quota_used"),
		index.Fields("expires_at"),
	}
}
