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
)

// SubscriptionPlanGroup stores the OpenAI groups a subscription plan can use.
type SubscriptionPlanGroup struct {
	ent.Schema
}

func (SubscriptionPlanGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "subscription_plan_groups"},
		field.ID("subscription_plan_id", "group_id"),
	}
}

func (SubscriptionPlanGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("subscription_plan_id"),
		field.Int64("group_id"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (SubscriptionPlanGroup) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("subscription_plan", SubscriptionPlan.Type).
			Unique().
			Required().
			Field("subscription_plan_id"),
		edge.To("group", Group.Type).
			Unique().
			Required().
			Field("group_id"),
	}
}

func (SubscriptionPlanGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id"),
	}
}
