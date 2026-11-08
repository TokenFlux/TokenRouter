package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
)

// creative_run_output.go defines creative image delivery metadata separately
// from the bytes kept in Redis. Expiration, acknowledgement, and error
// information let Queries decide whether an output can be served, while
// temporary bytes may be removed by acknowledgement, TTL, or terminal-run
// cleanup.

// CreativeRunOutput stores delivery metadata for one indexed creative image result.
type CreativeRunOutput struct {
	ent.Schema // generated entity definition
}

// Annotations maps creative output metadata to the creative_run_outputs table.
func (CreativeRunOutput) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "creative_run_outputs"},
	}
}

// Mixin supplies creation and update timestamps for creative output metadata.
func (CreativeRunOutput) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

// Fields describes output identity, delivery status, MIME, size, and expiry.
// Generated Ent creation starts pending. Output measurements, acknowledgement,
// and failure metadata remain nullable until the repository records those
// outcomes for the indexed result.
func (CreativeRunOutput) Fields() []ent.Field {
	return []ent.Field{
		// Observed GIF frames; static outputs retain the historical zero value.
		field.Int("frame_count").Default(0),
		// Associated run identity.
		field.String("run_id").MaxLen(64),
		// Run-local output position.
		field.Int("output_index"),
		// Output delivery state.
		field.String("status").MaxLen(20).Default("pending"),
		// Actual image MIME.
		field.String("mime_type").Optional().Nillable().MaxLen(128),
		// bytes
		field.Int64("byte_size").Optional().Nillable(),
		// transient_expires_at limits delivery in Queries, which treats an output
		// as expired when the query time is after this recorded timestamp.
		// Redis removal or acknowledgement can make the bytes unavailable earlier.
		field.Time("transient_expires_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// acked_at records acknowledgement accepted by the output repository.
		// Queries records that state before requesting deletion of the Redis
		// bytes, so this timestamp does not depend on deletion succeeding.
		field.Time("acked_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// Output failure code.
		field.String("error_code").Optional().Nillable().MaxLen(128),
		// Output failure description.
		field.String("error_message").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

// Indexes enforces one metadata row for each run and output index.
// Additional indexes support finding outputs by run delivery state and
// locating metadata with recorded expiration times.
func (CreativeRunOutput) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("run_id", "output_index").Unique(),
		index.Fields("run_id", "status"),
		index.Fields("transient_expires_at"),
	}
}
