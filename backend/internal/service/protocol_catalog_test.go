package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestProtocolCatalogFixture(t *testing.T) {
	catalog := domain.ProtocolCatalog()
	require.Len(t, catalog, 24)
	ids := map[domain.ProtocolID]bool{}
	public := 0
	for _, p := range catalog {
		require.False(t, ids[p.ID])
		ids[p.ID] = true
		if !p.UpstreamOnly {
			public++
		}
	}
	require.Equal(t, 21, public)
	require.Len(t, domain.AuxiliaryOperations(), 11)
	for _, operation := range domain.AuxiliaryOperations() {
		if operation.Protocol != "" {
			require.True(t, ids[operation.Protocol])
		}
	}
	if path := os.Getenv("PROTOCOL_CATALOG_TEST_FIXTURE"); path != "" {
		data, err := json.MarshalIndent(AdminProtocolCatalog(), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// 默认运行即核对前端夹具；更新目录后需显式导出并审查契约差异。
	frontend := filepath.Join("..", "..", "..", "frontend", "src")
	fixture, err := os.ReadFile(filepath.Join(frontend, "__tests__", "fixtures", "protocol-catalog.json"))
	require.NoError(t, err)
	actual, err := json.Marshal(AdminProtocolCatalog())
	require.NoError(t, err)
	require.JSONEq(t, string(fixture), string(actual))

	// TypeScript 联合类型必须覆盖完整目录，新增或删除协议都不能静默漂移。
	types, err := os.ReadFile(filepath.Join(frontend, "types", "index.ts"))
	require.NoError(t, err)
	block := regexp.MustCompile(`(?s)export type ProtocolID =\s*((?:\s*\|\s*'[^']+')+)`).FindSubmatch(types)
	require.Len(t, block, 2)
	frontendIDs := regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(block[1], -1)
	require.Len(t, frontendIDs, len(ids))
	for _, match := range frontendIDs {
		id := domain.ProtocolID(match[1])
		require.True(t, ids[id], "unknown or duplicated frontend protocol %s", id)
		delete(ids, id)
	}
	require.Empty(t, ids)
}
