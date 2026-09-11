package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	gatewayhttpapi "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routinghttpapi "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"

	"github.com/stretchr/testify/require"
)

func TestProtocolCatalogFixture(t *testing.T) {
	catalog := capability.ProtocolCatalog()
	require.Len(t, catalog, 24)
	ids := map[protocol.ProtocolID]bool{}
	public := 0
	for _, p := range catalog {
		require.False(t, ids[p.ID])
		ids[p.ID] = true
		if !p.UpstreamOnly {
			public++
		}
	}
	require.Equal(t, 21, public)
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
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// 默认运行即核对前端夹具；更新目录后需显式导出并审查契约差异。
	frontend := filepath.Join("..", "..", "..", "frontend", "src")
	fixture, err := os.ReadFile(filepath.Join(frontend, "__tests__", "fixtures", "protocol-catalog.json"))
	require.NoError(t, err)
	actual, err := json.Marshal(routinghttpapi.AdminProtocolCatalog(testEndpoints()))
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
		id := protocol.ProtocolID(match[1])
		require.True(t, ids[id], "unknown or duplicated frontend protocol %s", id)
		delete(ids, id)
	}
	require.Empty(t, ids)
}

// testEndpoints 与 app 一样显式组装跨 Adapter 的只读投影。
func testEndpoints() map[protocol.ProtocolID]string {
	out := map[protocol.ProtocolID]string{}
	for _, e := range gatewayhttpapi.ProtocolEndpoints() {
		out[e.ID] = e.Endpoint
	}
	return out
}
