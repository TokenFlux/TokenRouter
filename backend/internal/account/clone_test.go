package account

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/stretchr/testify/require"
)

// 复制必须保留载荷和值类型，不能把共享前缀的不同长度切片误认为同一容器。
func TestCloneValuesPreservesShapesAndIsolation(t *testing.T) {
	nested := []any{map[string]any{"number": json.Number("1.123456789")}, "second"}
	values := map[string]any{"short": nested[:1], "long": nested, "nil": []any(nil), "empty": []any{}, "raw": json.RawMessage(`{"ok":true}`)}
	before, err := json.Marshal(values)
	require.NoError(t, err)
	copied := CloneValues(values)
	after, err := json.Marshal(copied)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	copiedLong, ok := copied["long"].([]any)
	require.True(t, ok)
	copiedEntry, ok := copiedLong[0].(map[string]any)
	require.True(t, ok)
	originalEntry, ok := nested[0].(map[string]any)
	require.True(t, ok)
	require.IsType(t, json.Number(""), copiedEntry["number"])
	copiedEntry["number"] = json.Number("9")
	require.Equal(t, json.Number("1.123456789"), originalEntry["number"])
	copiedRaw, ok := copied["raw"].(json.RawMessage)
	require.True(t, ok)
	originalRaw, ok := values["raw"].(json.RawMessage)
	require.True(t, ok)
	copiedRaw[0] = '['
	require.Equal(t, byte('{'), originalRaw[0])

}
func TestCloneValuesRetainsInvalidCycleForEncodingError(t *testing.T) {
	values := map[string]any{}
	values["self"] = values
	_, err := json.Marshal(CloneValues(values))
	require.Error(t, err)
}
func TestCloneRecordIsolatesRelationsAndPrivateValues(t *testing.T) {
	expires := time.Now()
	source := &Record{ID: 1, GroupIDs: []int64{3}, Credentials: map[string]any{"token": "private-token"}, Extra: map[string]any{"cookie": "private-cookie"}, Proxy: &egress.Proxy{Password: "private-password", ExpiresAt: &expires}, Groups: []*accessview.GroupConfig{{ID: 3, ModelRouting: map[string][]int64{"model": {1}}}}}
	source.AccountGroups = []GroupMembership{{AccountID: 1, GroupID: 3, Account: source, Group: source.Groups[0]}}
	copied := CloneRecord(source)
	require.Same(t, copied, copied.AccountGroups[0].Account)
	copied.Credentials["token"] = "changed"
	copied.Groups[0].ModelRouting["model"][0] = 9
	copied.Proxy.Password = "changed"
	copied.Proxy.ExpiresAt = nil
	require.Equal(t, "private-token", source.Credentials["token"])
	require.Equal(t, int64(1), source.Groups[0].ModelRouting["model"][0])
	require.Equal(t, "private-password", source.Proxy.Password)
	require.NotNil(t, source.Proxy.ExpiresAt)
	require.NotContains(t, fmt.Sprintf("%+v %#v", source, source), "private-")
	source.AccountGroups = nil
	payload, err := json.Marshal(source)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "private-")
}

// 统一协议规范化会产生具名切片，复制不能只覆盖 JSON 解码得到的 []any。
func TestCloneValuesCopiesNormalizedProtocolIDs(t *testing.T) {
	protocols := []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}
	cloned := CloneValues(map[string]any{"upstream_protocols": protocols})
	copied, ok := cloned["upstream_protocols"].([]protocol.ProtocolID)
	if !ok {
		t.Fatal("协议切片类型发生改变")
	}
	copied[0] = protocol.ProtocolAnthropicMessages
	if protocols[0] != protocol.ProtocolOpenAIResponses {
		t.Fatal("协议副本污染原值")
	}
}

// 原地应用必须保留根自引用；地址稳定使既有方法绑定继续读取更新后的记录。
func TestCopyRecordIntoKeepsRootAndSupportsSameRecord(t *testing.T) {
	source := &Record{ID: 1, Credentials: map[string]any{"token": "source"}}
	source.AccountGroups = []GroupMembership{{Account: source}}
	var target Record
	CopyRecordInto(&target, source)
	require.Same(t, &target, target.AccountGroups[0].Account)
	target.Credentials["token"] = "target"
	require.Equal(t, "source", source.GetCredential("token"))
	CopyRecordInto(&target, &target)
	require.Same(t, &target, target.AccountGroups[0].Account)
	require.Equal(t, "target", target.GetCredential("token"))
	CopyRecordInto(&target, nil)
	require.Zero(t, target.ID)
	require.Nil(t, target.Credentials)
	require.Nil(t, target.AccountGroups)
}
