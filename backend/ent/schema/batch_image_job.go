package schema

import (
	"time"

	"github.com/BrandonVee/TokenRouter/internal/domain"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BatchImageJob 定义异步批量图片任务的数据结构。
//
// 删除策略：账务源保留
// 这张表是批量生图任务的账务和状态源；用户侧删除仅通过 user_deleted_at
// 从列表隐藏，输出清理通过 output_deleted 状态和删除时间字段表达。
type BatchImageJob struct {
	ent.Schema
}

func (BatchImageJob) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "batch_image_jobs"},
	}
}

func (BatchImageJob) Fields() []ent.Field {
	return []ent.Field{
		field.String("batch_id").MaxLen(64).Immutable(),
		field.Int64("user_id"),
		// 仓储层会显式补齐付款人；Optional 仅兼容测试和滚动升级期间的旧 Ent 调用。
		field.Int64("billing_user_id").Optional(),
		field.Int64("team_id").Optional().Nillable(),
		field.Int64("api_key_id").Optional().Nillable(),
		field.Int64("account_id").Optional().Nillable(),
		// 批任务会冻结 API Key 提交时的结算来源，避免异步结算读取到后续编辑后的配置。
		field.String("billing_mode").MaxLen(32).Default("auto"),
		field.Int64("preferred_subscription_id").Optional().Nillable(),
		field.String("provider").MaxLen(32),
		field.String("model").MaxLen(128),
		field.String("task_name").MaxLen(255).Default(""),
		field.String("status").MaxLen(32).Default("created"),
		field.String("provider_job_name").Optional().Nillable().MaxLen(512),
		field.String("provider_input_ref").Optional().Nillable().MaxLen(1024),
		field.String("provider_output_ref").Optional().Nillable().MaxLen(1024),
		field.String("gcs_input_uri").Optional().Nillable().MaxLen(1024),
		field.String("gcs_output_uri").Optional().Nillable().MaxLen(1024),
		field.Int("item_count"),
		field.Int("success_count").Default(0),
		field.Int("fail_count").Default(0),
		field.Int("cancelled_count").Default(0),
		field.Float("estimated_cost").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		field.Float("hold_amount").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		field.Float("actual_cost").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// 批量任务先占订阅、再冻结余额；这两个字段共同构成完整预占快照。
		field.Float("balance_hold_amount").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		field.JSON("subscription_hold_allocations", []domain.BillingAllocation{}).
			Default(func() []domain.BillingAllocation { return []domain.BillingAllocation{} }).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		// 分别快照订阅默认倍率和按量倍率，供混合结算按来源还原价格。
		field.Float("subscription_rate_multiplier").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(1),
		field.Float("balance_rate_multiplier").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(1),
		field.Bool("plan_group_rate_multiplier_enabled").Default(true),
		// 标记当前任务是否已按预计金额预记 Key 和团队成员额度。
		field.Bool("allowance_reserved").Default(false),
		field.String("currency").MaxLen(16).Default("USD"),
		field.String("hold_id").Optional().Nillable().MaxLen(128),
		field.String("idempotency_key").Optional().Nillable().MaxLen(255),
		field.String("request_hash").Optional().Nillable().MaxLen(128),
		field.String("manifest_hash").Optional().Nillable().MaxLen(128),
		field.Int("retry_count").Default(0),
		field.Int("version").Default(0),
		field.Time("output_expires_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("input_deleted_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("output_deleted_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("downloaded_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("user_deleted_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("last_error_code").Optional().Nillable().MaxLen(128),
		field.String("last_error_message").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("submitted_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("started_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("finished_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("settled_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (BatchImageJob) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("batch_id").Unique(),
		index.Fields("user_id", "created_at"),
		index.Fields("billing_user_id", "created_at"),
		index.Fields("team_id", "created_at"),
		index.Fields("status"),
		index.Fields("provider", "status"),
		index.Fields("idempotency_key").Annotations(entsql.IndexWhere("idempotency_key IS NOT NULL AND idempotency_key <> ''")),
		index.Fields("manifest_hash").Unique().Annotations(entsql.IndexWhere("manifest_hash IS NOT NULL AND manifest_hash <> ''")),
		index.Fields("output_expires_at"),
		index.Fields("downloaded_at"),
		index.Fields("user_deleted_at"),
	}
}
