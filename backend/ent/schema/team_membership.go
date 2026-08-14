package schema

import (
	"fmt"
	"time"

	"github.com/BrandonVee/TokenRouter/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TeamMembership 保存用户在团队内的角色、限额和自然周期用量。
type TeamMembership struct {
	ent.Schema
}

func (TeamMembership) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "team_memberships"}}
}

func (TeamMembership) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (TeamMembership) Fields() []ent.Field {
	validateNonNegative := func(value float64) error {
		if value < 0 {
			return fmt.Errorf("团队成员限额和用量不能为负数")
		}
		return nil
	}
	decimalLimit := func(name string) ent.Field {
		return field.Float(name).SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).Default(0).Validate(validateNonNegative)
	}
	decimalUsage := func(name string) ent.Field {
		return field.Float(name).SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0).Validate(validateNonNegative)
	}
	return []ent.Field{
		field.Int64("team_id"),
		field.Int64("user_id"),
		field.String("role").MaxLen(20).Default("member").Validate(func(value string) error {
			if value != "owner" && value != "member" {
				return fmt.Errorf("团队角色必须为 owner 或 member")
			}
			return nil
		}),
		decimalLimit("daily_limit_usd"),
		decimalLimit("weekly_limit_usd"),
		decimalLimit("monthly_limit_usd"),
		decimalUsage("daily_usage_usd"),
		decimalUsage("weekly_usage_usd"),
		decimalUsage("monthly_usage_usd"),
		field.Time("daily_window_start").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("weekly_window_start").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("monthly_window_start").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("joined_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("left_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (TeamMembership) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("team", Team.Type).Ref("memberships").Field("team_id").Unique().Required(),
		edge.From("user", User.Type).Ref("team_memberships").Field("user_id").Unique().Required(),
	}
}

func (TeamMembership) Indexes() []ent.Index {
	return []ent.Index{index.Fields("team_id"), index.Fields("user_id"), index.Fields("team_id", "role")}
}
