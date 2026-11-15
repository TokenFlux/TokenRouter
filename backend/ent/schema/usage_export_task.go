package schema

import (
	"encoding/json"
	"fmt"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
)

// usage_export_task.go defines persistent user and administrator CSV export
// tasks with serialized filter snapshots and artifact lifecycle metadata. Task
// rows retain row progress, local artifact location, cancellation actors, and
// expiration timestamps so background workers can stream exports and sweep
// expired files across process restarts.

// UsageExportTask stores usage-record export requests, row progress, and artifact lifecycle state.
type UsageExportTask struct {
	ent.Schema // generated entity definition
}

// Annotations maps export task persistence to the usage_export_tasks database table.
func (UsageExportTask) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "usage_export_tasks"},
	}
}

// Mixin supplies insertion and progress-update timestamps for usage export tasks.
func (UsageExportTask) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

// Fields describes the export scope, filter snapshot, row counters, and artifact lifecycle columns.
// Callers supply scope, status, serialized filters, and creator identity. Row
// and byte counters start at zero; file paths, cancellation details, failure
// text, and execution or expiration timestamps stay nullable until recorded.
func (UsageExportTask) Fields() []ent.Field {
	return []ent.Field{
		// scope distinguishes self-service user exports from administrator exports
		// so authorization and active-task quotas evaluate each audience separately.
		field.String("scope").
			MaxLen(16).
			Validate(validateUsageExportScope),
		// status is pending before claim, running while writing batches, succeeded
		// once the CSV is committed, failed after an execution error, canceled
		// when stopped by an authorized actor, or expired after artifact cleanup.
		field.String("status").
			MaxLen(20).
			Validate(validateUsageExportStatus),
		// Serialized export filter snapshot.
		field.JSON("filters", json.RawMessage{}),
		// Requesting user or administrator identity.
		field.Int64("created_by"),
		// total_rows and exported_rows track batch progress for active and finished tasks.
		// Both start at zero before the worker counts matching usage rows and writes CSV batches.
		field.Int64("total_rows").
			Default(0), // matched row count snapshot
		field.Int64("exported_rows").
			Default(0), // rows written so far
		// file_name, file_path, and file_size_bytes describe the generated CSV artifact.
		// Names and store-relative keys stay unset until completion, and file_path is cleared when expired.
		field.String("file_name").
			Optional().
			Nillable(), // download attachment filename
		field.String("file_path").
			Optional().
			Nillable(), // artifact-store relative key
		field.Int64("file_size_bytes").
			Default(0), // committed CSV byte length
		// Recorded execution failure message.
		field.String("error_message").
			Optional().
			Nillable(),
		// canceled_by and canceled_at record an explicit stop request together.
		// They remain unset until a user or administrator cancels a pending or running task.
		field.Int64("canceled_by").
			Optional().
			Nillable(), // canceling user identity
		field.Time("canceled_at").
			Optional().
			Nillable(), // cancellation request timestamp
		// First worker claim timestamp.
		field.Time("started_at").
			Optional().
			Nillable(),
		// Terminal execution timestamp.
		field.Time("finished_at").
			Optional().
			Nillable(),
		// Scheduled artifact retention deadline.
		field.Time("expires_at").
			Optional().
			Nillable(),
	}
}

// Indexes supports worker claiming, scoped chronological listing, and expired-artifact sweeps.
func (UsageExportTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "created_at"),
		index.Fields("scope", "created_by", "created_at"),
		index.Fields("scope", "created_at"),
		index.Fields("status", "expires_at"),
	}
}

// validateUsageExportScope accepts only user and admin export audiences.
// Values are matched exactly; unsupported scope strings fail generated Ent
// validation before a mutation reaches the database.
func validateUsageExportScope(scope string) error {
	switch scope {
	case "user", "admin":
		return nil
	default:
		return fmt.Errorf("invalid usage export scope: %s", scope)
	}
}

// validateUsageExportStatus accepts the six persisted export lifecycle states.
// Values are matched exactly; unsupported status strings return an error
// during generated Ent field validation before the mutation is saved.
func validateUsageExportStatus(status string) error {
	switch status {
	case "pending", "running", "succeeded", "failed", "canceled", "expired":
		return nil
	default:
		return fmt.Errorf("invalid usage export status: %s", status)
	}
}
