package service

import (
	"encoding/json"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestProtocolCatalogFixture(t *testing.T) {
	catalog := domain.ProtocolCatalog()
	require.Len(t, catalog, 24)
	ids := map[GroupClientProtocol]bool{}
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
}
