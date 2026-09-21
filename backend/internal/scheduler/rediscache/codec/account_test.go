package codec

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/stretchr/testify/require"
)

func TestAccountWirePreservesHistoricalFields(t *testing.T) {
	value := &account.Record{ID: 1, Credentials: map[string]any{"access_token": "fixture-only"}, Extra: map[string]any{"counter": float64(3)}, Groups: []*accessview.GroupConfig{{ID: 7, AllowedProtocols: nil}}, AccountGroups: []account.GroupMembership{}}
	raw, err := MarshalAccountRecord(value)
	require.NoError(t, err)
	var stored map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.JSONEq(t, `{"access_token":"fixture-only"}`, string(stored["Credentials"]))
	require.Equal(t, "[]", string(stored["AccountGroups"]))
	var groups []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(stored["Groups"], &groups))
	require.Equal(t, "null", string(groups[0]["AccountGroups"]))
	require.Equal(t, "null", string(groups[0]["AllowedProtocols"]))
	decoded, err := UnmarshalAccountRecord(raw)
	require.NoError(t, err)
	require.Equal(t, value.Credentials, decoded.Credentials)
	require.NotNil(t, decoded.AccountGroups)
	require.Empty(t, decoded.AccountGroups)
	require.Equal(t, value.Groups, decoded.Groups)
	// 公开账号 JSON 仍不包含执行凭据，只有存储编码显式保留。
	public, err := json.Marshal(value)
	require.NoError(t, err)
	require.NotContains(t, string(public), "fixture-only")
}

func TestAccountWireReadsExistingNestedGroup(t *testing.T) {
	raw := []byte(`{"ID":1,"Credentials":{"api_key":"fixture-only"},"Groups":null,"AccountGroups":[{"AccountID":1,"GroupID":7,"Account":null,"Group":{"ID":7,"Name":"historical","AccountGroups":null,"AllowedProtocols":[]}}]}`)
	decoded, err := UnmarshalAccountRecord(raw)
	require.NoError(t, err)
	require.Nil(t, decoded.Groups)
	require.Len(t, decoded.AccountGroups, 1)
	require.Equal(t, "historical", decoded.AccountGroups[0].Group.Name)
	require.NotNil(t, decoded.AccountGroups[0].Group.AllowedProtocols)
	again, err := MarshalAccountRecord(decoded)
	require.NoError(t, err)
	var stored map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(again, &stored))
	require.Contains(t, string(stored["AccountGroups"]), `"AccountGroups":null`)
}
