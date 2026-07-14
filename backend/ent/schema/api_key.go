package schema

import (
	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/internal/domain"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// APIKey holds the schema definition for the APIKey entity.
type APIKey struct {
	ent.Schema
}

func (APIKey) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "api_keys"},
	}
}

func (APIKey) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (APIKey) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.String("key").
			MaxLen(128).
			NotEmpty().
			Unique(),
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		field.Int64("group_id").
			Optional().
			Nillable(),
		field.String("status").
			MaxLen(20).
			Default(domain.StatusActive),
		// 单个 API Key 的 Fast 模式策略，默认保持下游请求的现有行为。
		field.String("fast_mode_policy").
			MaxLen(32).
			Default("follow_request").
			Comment("API Key 的 Fast 模式策略：follow_request、force_on 或 force_off"),
		field.Time("last_used_at").
			Optional().
			Nillable().
			Comment("Last usage time of this API key"),
		field.JSON("ip_whitelist", []string{}).
			Optional().
			Comment("Allowed IPs/CIDRs, e.g. [\"192.168.1.100\", \"10.0.0.0/8\"]"),
		field.JSON("ip_blacklist", []string{}).
			Optional().
			Comment("Blocked IPs/CIDRs"),

		// ========== Quota fields ==========
		// Quota limit in USD (0 = unlimited)
		field.Float("quota").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Quota limit in USD for this API key (0 = unlimited)"),
		// Used quota amount
		field.Float("quota_used").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Used quota amount in USD"),
		// Expiration time (nil = never expires)
		field.Time("expires_at").
			Optional().
			Nillable().
			Comment("Expiration time for this API key (null = never expires)"),

		// ========== Rate limit fields ==========
		// Rate limit configuration (0 = unlimited)
		field.Float("rate_limit_5h").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Rate limit in USD per 5 hours (0 = unlimited)"),
		field.Float("rate_limit_1d").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Rate limit in USD per day (0 = unlimited)"),
		field.Float("rate_limit_7d").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Rate limit in USD per 7 days (0 = unlimited)"),
		// Rate limit usage tracking
		field.Float("usage_5h").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Used amount in USD for the current 5h window"),
		field.Float("usage_1d").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Used amount in USD for the current 1d window"),
		field.Float("usage_7d").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("Used amount in USD for the current 7d window"),
		// Window start times
		field.Time("window_5h_start").
			Optional().
			Nillable().
			Comment("Start time of the current 5h rate limit window"),
		field.Time("window_1d_start").
			Optional().
			Nillable().
			Comment("Start time of the current 1d rate limit window"),
		field.Time("window_7d_start").
			Optional().
			Nillable().
			Comment("Start time of the current 7d rate limit window"),

		// 用户确认过的数据共享须知版本，用于防止未读须知直接切换到数据共享分组。
		field.Int("data_sharing_notice_version").
			Default(0).
			Comment("用户已确认的数据共享须知版本，0 表示未确认"),
		field.Int64("data_sharing_confirmed_group_id").
			Optional().
			Nillable().
			Comment("最近一次确认的数据共享目标分组 ID"),
		field.Time("data_sharing_confirmed_at").
			Optional().
			Nillable().
			Comment("最近一次确认数据共享须知的时间"),
		// 绑定分组停用时是否允许请求级回退到同平台默认分组。
		field.Bool("fallback_to_default_group_when_unavailable").
			Default(true).
			Comment("绑定分组不可用时自动回退到同平台默认分组"),
	}
}

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
	}
}

func (APIKey) Indexes() []ent.Index {
	return []ent.Index{
		// key 字段已在 Fields() 中声明 Unique()，无需重复索引
		index.Fields("user_id"),
		index.Fields("group_id"),
		index.Fields("status"),
		index.Fields("deleted_at"),
		index.Fields("last_used_at"),
		// Index for quota queries
		index.Fields("quota", "quota_used"),
		index.Fields("expires_at"),
		index.Fields("data_sharing_confirmed_group_id"),
	}
}
