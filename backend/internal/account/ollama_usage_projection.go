// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	json "encoding/json"
	strings "strings"
)

func OllamaCloudUsageStateFromAccount(account *Record) *OllamaCloudUsageState {
	state := &OllamaCloudUsageState{}
	if account == nil {
		return state
	}
	state.AccountID = account.ID
	state.Eligible = IsOllamaCloudUsageAccount(account)
	if !state.Eligible {
		return state
	}
	state.Configured = OllamaCloudUsageConfigured(account)
	state.AutoRefreshEnabled = state.Configured && OllamaCloudUsageAutoRefreshEnabled(account)
	state.Snapshot = DecodeOllamaCloudUsageSnapshot(account.Extra)
	return state
}

func OllamaCloudUsageConfigured(account *Record) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	value, ok := account.Extra[OllamaCloudUsageSessionExtraKey].(string)
	return ok && strings.TrimSpace(value) != ""
}

func OllamaCloudUsageAutoRefreshEnabled(account *Record) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	enabled, ok := account.Extra[OllamaCloudUsageAutoRefreshExtraKey].(bool)
	return ok && enabled
}

func DecodeOllamaCloudUsageSnapshot(extra map[string]any) *OllamaCloudUsageSnapshot {
	if extra == nil {
		return nil
	}
	value, ok := extra[OllamaCloudUsageSnapshotExtraKey]
	if !ok || value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var snapshot OllamaCloudUsageSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil
	}
	if snapshot.Status != OllamaCloudUsageStatusOK && snapshot.Status != OllamaCloudUsageStatusUnauthorized && snapshot.Status != OllamaCloudUsageStatusFailed {
		return nil
	}
	return &snapshot
}
