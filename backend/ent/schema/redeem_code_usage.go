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

// RedeemCodeUsage holds the schema definition for the RedeemCodeUsage entity.
type RedeemCodeUsage struct {
	ent.Schema
}

func (RedeemCodeUsage) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "redeem_code_usages"},
	}
}

func (RedeemCodeUsage) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("redeem_code_id"),
		field.Int64("user_id"),
		field.Time("used_at").
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (RedeemCodeUsage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("redeem_code", RedeemCode.Type).
			Ref("usage_records").
			Field("redeem_code_id").
			Required().
			Unique(),
		edge.From("user", User.Type).
			Ref("redeem_code_usages").
			Field("user_id").
			Required().
			Unique(),
	}
}

func (RedeemCodeUsage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("redeem_code_id"),
		index.Fields("user_id"),
		index.Fields("redeem_code_id", "user_id").Unique(),
	}
}
