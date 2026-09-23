package postgres

import (
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/stretchr/testify/require"
)

// Ent 行投影不能让返回记录的嵌套凭据修改反向污染原行对象。
func TestS06AccountEntityProjectionIsolatesNestedCredentials(t *testing.T) {
	entity := &dbent.Account{ID: 1, Credentials: map[string]any{"extension": map[string]any{"value": "original"}}}
	projected := RecordFromEntity(entity)
	copy, ok := projected.Credentials["extension"].(map[string]any)
	require.True(t, ok)
	copy["value"] = "modified"
	original, ok := entity.Credentials["extension"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "original", original["value"])
}
