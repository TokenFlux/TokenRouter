package schema

import (
	"fmt"

	"github.com/BrandonVee/TokenRouter/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TeamOwnershipTransfer 保存需要目标成员确认的所有权转让。
type TeamOwnershipTransfer struct {
	ent.Schema
}

func (TeamOwnershipTransfer) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "team_ownership_transfers"}}
}

func (TeamOwnershipTransfer) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (TeamOwnershipTransfer) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("team_id"),
		field.Int64("from_user_id"),
		field.Int64("to_user_id"),
		field.String("token_hash").MaxLen(64).NotEmpty(),
		field.String("status").MaxLen(20).Default("pending").Validate(func(value string) error {
			switch value {
			case "pending", "accepted", "declined", "cancelled", "expired":
				return nil
			default:
				return fmt.Errorf("无效的所有权转让状态")
			}
		}),
		field.Time("expires_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("resolved_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (TeamOwnershipTransfer) Edges() []ent.Edge {
	return []ent.Edge{edge.From("team", Team.Type).Ref("ownership_transfers").Field("team_id").Unique().Required()}
}

func (TeamOwnershipTransfer) Indexes() []ent.Index {
	return []ent.Index{index.Fields("token_hash").Unique(), index.Fields("team_id"), index.Fields("to_user_id", "status")}
}
