package provider

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 执行目标只持有原生图；复制边界保留根关联，并隔离外部凭据 map。
func TestExecutionAccountPreservesNativeGraphAndIsolation(t *testing.T) {
	source := &account.Record{ID: 1, Credentials: map[string]any{"access_token": "original"}}
	source.AccountGroups = []account.GroupMembership{{AccountID: 1, GroupID: 3, Account: source}}
	target := NewExecutionAccount(source)
	require.Same(t, &target.Record, target.Record.AccountGroups[0].Account)
	target.Record.Credentials["access_token"] = "target"
	require.Equal(t, "original", source.GetCredential("access_token"))
	snapshot := ExecutionRecord(target)
	require.Same(t, snapshot, snapshot.AccountGroups[0].Account)
	snapshot.Credentials["access_token"] = "snapshot"
	require.Equal(t, "target", target.View().GetCredential("access_token"))
	payload, err := json.Marshal(target)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(payload), "执行凭据与路线不能作为持久或公开载荷")
}

// 按值复制仍允许当次替换字段；先绑定的方法必须看到原持有者的后续应用结果。
func TestExecutionAccountValueCopyAndBoundReader(t *testing.T) {
	shared := NewExecutionAccount(&account.Record{ID: 1, Credentials: map[string]any{"access_token": "shared"}})
	attempt := *shared
	attempt.Record.Credentials = map[string]any{"access_token": "attempt"}
	require.Equal(t, "shared", shared.View().GetCredential("access_token"))
	read := attempt.View().GetCredential
	ApplyExecutionRecord(&attempt, &account.Record{ID: 1, Credentials: map[string]any{"access_token": "updated"}})
	require.Equal(t, "updated", read("access_token"))
	var absent *ExecutionAccount
	require.Nil(t, absent.View())
	require.Nil(t, ExecutionRecord(absent))
	require.Nil(t, ExecutionAccounts(nil))
	require.Nil(t, ExecutionRecords(nil))
}
