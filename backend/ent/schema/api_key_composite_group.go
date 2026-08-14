package schema

import (
	"github.com/BrandonVee/TokenRouter/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// APIKeyCompositeGroup 保存复合 API Key 的分组前缀映射。
type APIKeyCompositeGroup struct {
	ent.Schema
}

func (APIKeyCompositeGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "api_key_composite_groups"}}
}

func (APIKeyCompositeGroup) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (APIKeyCompositeGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("api_key_id"),
		field.Int64("group_id"),
		field.String("prefix").MaxLen(32).NotEmpty(),
		field.String("normalized_prefix").MaxLen(32).NotEmpty(),
		field.Int("sort_order").Default(0),
		field.Int("data_sharing_notice_version").Default(0),
		field.Time("data_sharing_confirmed_at").Optional().Nillable(),
	}
}

func (APIKeyCompositeGroup) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("api_key", APIKey.Type).
			Ref("composite_groups").
			Field("api_key_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("group", Group.Type).
			Ref("api_key_composite_groups").
			Field("group_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (APIKeyCompositeGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("api_key_id", "group_id").Unique(),
		index.Fields("api_key_id", "normalized_prefix").Unique(),
		index.Fields("group_id"),
		index.Fields("api_key_id", "sort_order"),
	}
}
