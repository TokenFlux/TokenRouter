package app

// 本文件检查 http_routes_admin.go 与 http_modules_wire.go 组合的协议目录和前端协议定义。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routinghttpapi "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
)

func TestProtocolCatalogFixture(t *testing.T) {
	catalog := capability.ProtocolCatalog()
	require.Len(t, catalog, 25)
	ids := map[protocol.ProtocolID]bool{}
	public := 0
	for _, p := range catalog {
		require.False(t, ids[p.ID])
		ids[p.ID] = true
		if !p.UpstreamOnly {
			public++
		}
	}
	require.Equal(t, 22, public)
	require.Len(t, routinghttpapi.AuxiliaryOperations(), 11)
	for _, operation := range routinghttpapi.AuxiliaryOperations() {
		if operation.Protocol != "" {
			require.True(t, ids[operation.Protocol])
		}
	}
	if path := os.Getenv("PROTOCOL_CATALOG_TEST_FIXTURE"); path != "" {
		data, err := json.MarshalIndent(routinghttpapi.AdminProtocolCatalog(testEndpoints()), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 测试默认核对前端夹具，更新协议目录后需要导出并检查字段差异。
	frontend := filepath.Join("..", "..", "..", "frontend", "src")
	fixture, err := os.ReadFile(filepath.Join(frontend, "__tests__", "fixtures", "protocol-catalog.json"))
	require.NoError(t, err)
	actual, err := json.Marshal(routinghttpapi.AdminProtocolCatalog(testEndpoints()))
	require.NoError(t, err)
	require.JSONEq(t, string(fixture), string(actual))

	// TypeScript 联合类型需要覆盖协议目录，增删协议后同步更新。
	types, err := os.ReadFile(filepath.Join(frontend, "types", "index.ts"))
	require.NoError(t, err)
	block := regexp.MustCompile(`(?s)export type ProtocolID =\s*((?:\s*\|\s*'[^']+')+)`).FindSubmatch(types)
	require.Len(t, block, 2)
	frontendIDs := regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(block[1], -1)
	require.Len(t, frontendIDs, len(ids))
	for _, match := range frontendIDs {
		id := protocol.ProtocolID(match[1])
		require.True(t, ids[id], "unknown or duplicated frontend protocol %s", id)
		delete(ids, id)
	}
	require.Empty(t, ids)
}
