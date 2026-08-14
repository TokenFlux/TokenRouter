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

// TeamInvitation 保存绑定受邀邮箱的短期团队邀请。
type TeamInvitation struct {
	ent.Schema
}

func (TeamInvitation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "team_invitations"}}
}

func (TeamInvitation) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (TeamInvitation) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("team_id"),
		field.Int64("inviter_user_id"),
		field.String("email").MaxLen(255).NotEmpty(),
		field.String("token_hash").MaxLen(64).NotEmpty(),
		field.String("status").MaxLen(20).Default("pending").Validate(func(value string) error {
			switch value {
			case "pending", "accepted", "declined", "revoked", "expired":
				return nil
			default:
				return fmt.Errorf("无效的团队邀请状态")
			}
		}),
		field.Time("expires_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int64("accepted_by_user_id").Optional().Nillable(),
		field.Time("accepted_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (TeamInvitation) Edges() []ent.Edge {
	return []ent.Edge{edge.From("team", Team.Type).Ref("invitations").Field("team_id").Unique().Required()}
}

func (TeamInvitation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("token_hash").Unique(), index.Fields("team_id"), index.Fields("email", "status", "expires_at")}
}
