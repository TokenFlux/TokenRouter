package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// DataShareSession 保存数据共享分组采集到的完整 Agent session。
type DataShareSession struct {
	ent.Schema
}

func (DataShareSession) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "data_share_sessions"},
	}
}

func (DataShareSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("trajectory_id").MaxLen(128).NotEmpty().Unique(),
		field.String("session_id").MaxLen(256).NotEmpty(),
		field.String("dataset").MaxLen(128).NotEmpty(),
		field.String("provider").MaxLen(50).NotEmpty(),
		field.String("model").MaxLen(100).NotEmpty(),
		field.String("request_path").MaxLen(128).Default(""),
		field.String("user_agent").MaxLen(512).Default(""),
		field.String("status").MaxLen(20).Default("completed"),
		field.Bool("is_final_snapshot").Default(true),
		field.Int("source_request_count").Default(0),
		field.String("system_prompt").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.JSON("tools", []map[string]any{}).Optional().SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.JSON("messages", []map[string]any{}).Optional().SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.JSON("usage", map[string]any{}).Optional().SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.JSON("meta", map[string]any{}).Optional().SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.JSON("session_json", map[string]any{}).Optional().SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.Bytes("payload_compressed").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "bytea"}),
		field.String("payload_encoding").MaxLen(20).Default(""),
		field.Int64("payload_bytes").Default(0),
		field.Bool("exportable").Default(false),
		field.String("quality_status").MaxLen(20).Default("invalid"),
		field.JSON("quality_errors", []string{}).Optional().SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.Int64("storage_bytes").Default(0),
		field.Int64("input_tokens").Default(0),
		field.Int64("output_tokens").Default(0),
		field.Int64("total_tokens").Default(0),
		// ActualCost 记录已知的用户实际扣费积分；NULL 表示历史 session 未采集扣费信息。
		field.Float("actual_cost").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		field.Int64("user_id"),
		field.Int64("api_key_id"),
		field.Int64("group_id"),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("ended_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (DataShareSession) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("session_id"),
		index.Fields("user_id"),
		index.Fields("api_key_id"),
		index.Fields("group_id"),
		index.Fields("provider"),
		index.Fields("model"),
		index.Fields("request_path"),
		index.Fields("user_agent"),
		index.Fields("exportable"),
		index.Fields("quality_status"),
		index.Fields("created_at"),
		index.Fields("created_at", "id"),
		index.Fields("updated_at"),
	}
}
