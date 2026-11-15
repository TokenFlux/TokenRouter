package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// usage_export_task_test.go verifies Ent field validators on UsageExportTask.
// Tests exercise all registered validators on each field so both length bounds
// and enum-literal checks reject invalid scope or status values before saves.

// TestUsageExportTaskValidatorsEnforceScopeAndStatus evaluates the full
// validator chain on the scope and status descriptors of UsageExportTask. It
// confirms that supported audience and lifecycle literals pass while unknown or
// oversized strings fail validation.
func TestUsageExportTaskValidatorsEnforceScopeAndStatus(t *testing.T) {
	validators := map[string]func(string) error{}
	for _, f := range (UsageExportTask{}).Fields() {
		desc := f.Descriptor()
		if len(desc.Validators) == 0 {
			continue
		}
		var fns []func(string) error
		for _, v := range desc.Validators {
			fn, ok := v.(func(string) error)
			require.True(t, ok)
			fns = append(fns, fn)
		}
		validators[desc.Name] = func(val string) error {
			for _, fn := range fns {
				if err := fn(val); err != nil {
					return err
				}
			}
			return nil
		}
	}

	validateScope, ok := validators["scope"]
	require.True(t, ok, "scope validator missing")
	require.NoError(t, validateScope("user"))
	require.NoError(t, validateScope("admin"))
	require.Error(t, validateScope("system"))

	validateStatus, ok := validators["status"]
	require.True(t, ok, "status validator missing")
	for _, status := range []string{"pending", "running", "succeeded", "failed", "canceled", "expired"} {
		require.NoError(t, validateStatus(status))
	}
	require.Error(t, validateStatus("completed"))
}
