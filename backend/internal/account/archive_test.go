package account

import (
	"context"
	"errors"
	"github.com/TokenFlux/TokenRouter/internal/account/transfer"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type archiveAccountsFixture struct {
	ArchiveAccounts
	values  []*Record
	events  *[]string
	created []CreateAccountInput
}

func (f *archiveAccountsFixture) GetAccountsByIDs(context.Context, []int64) ([]*Record, error) {
	*f.events = append(*f.events, "accounts")
	return f.values, nil
}
func (f *archiveAccountsFixture) CreateAccount(_ context.Context, input *CreateAccountInput) (*Record, error) {
	*f.events = append(*f.events, "create")
	f.created = append(f.created, *input)
	if input.Name == "bad" {
		return nil, errors.New("fixture rejected")
	}
	return &Record{ID: 1, Name: input.Name, Platform: input.Platform, Type: input.Type}, nil
}

type archiveProxiesFixture struct {
	ArchiveProxies
	events *[]string
}

func (f archiveProxiesFixture) ImportForAccountBinding(context.Context, []egress.TransferProxy) (map[string]int64, egress.ProxyImportResult, error) {
	*f.events = append(*f.events, "proxies")
	return map[string]int64{"known": 7}, egress.ProxyImportResult{ProxyCreated: 1}, nil
}

// include_proxies 的报错仍发生在账号读取和影子过滤之后；导出副本含显式备份凭据但不可回写来源。
func TestArchiveExportPreservesOrderAndIndependentSecrets(t *testing.T) {
	events := []string{}
	mapping := map[string]any{"alias": "upstream"}
	records := &archiveAccountsFixture{events: &events, values: []*Record{{ID: 1, Credentials: map[string]any{"api_key": "fixture-secret", "model_mapping": mapping}}}}
	archive := NewArchive(records, nil, ArchiveOptions{Now: func() time.Time { return time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC) }})
	sentinel := errors.New("invalid include_proxies fixture")
	_, err := archive.Export(context.Background(), ArchiveExportQuery{IDs: []int64{1}, IncludeProxies: func() (bool, error) { events = append(events, "include"); return true, sentinel }})
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, []string{"accounts", "include"}, events)
	payload, err := archive.Export(context.Background(), ArchiveExportQuery{IDs: []int64{1}, IncludeProxies: func() (bool, error) { return false, nil }})
	require.NoError(t, err)
	require.Equal(t, "2026-09-13T00:00:00Z", payload.ExportedAt)
	require.Empty(t, payload.Proxies)
	require.NotNil(t, payload.Proxies)
	require.Equal(t, "fixture-secret", payload.Accounts[0].Credentials["api_key"])
	copied, ok := payload.Accounts[0].Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	copied["alias"] = "changed"
	require.Equal(t, "upstream", mapping["alias"])
}

// 代理部分已成功后才读取一次动态模板；账号逐项失败保留原部分成功与输入隔离。
func TestArchiveImportRetainsPartialResultsAndLazyDefaults(t *testing.T) {
	events := []string{}
	records := &archiveAccountsFixture{events: &events}
	options := ArchiveOptions{Defaults: func(context.Context) (*transfer.OpenAIOAuthImportDefaults, error) {
		events = append(events, "defaults")
		return &transfer.OpenAIOAuthImportDefaults{Credentials: map[string]any{"model_mapping": map[string]any{"alias": "target"}}}, nil
	}, DecodeIDToken: func(string) (*ArchiveIdentityHints, error) {
		events = append(events, "decode")
		return &ArchiveIdentityHints{Email: "decoded@example.test"}, nil
	}}
	archive := NewArchive(records, archiveProxiesFixture{events: &events}, options)
	input := transfer.DataImportRequest{Data: transfer.DataPayload{Proxies: []transfer.DataProxy{}, Accounts: []transfer.DataAccount{{Name: "good", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"id_token": "fixture"}}, {Name: "bad", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture"}}}}}
	result, err := archive.Import(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 1, result.ProxyCreated)
	require.Equal(t, 1, result.AccountCreated)
	require.Equal(t, 1, result.AccountFailed)
	require.Equal(t, []string{"proxies", "defaults", "decode", "create", "create"}, events)
	require.Equal(t, map[string]any{"id_token": "fixture"}, input.Data.Accounts[0].Credentials)
	require.Equal(t, "decoded@example.test", records.created[0].Credentials["email"])
	require.True(t, records.created[0].SkipDefaultGroupBind)
}
