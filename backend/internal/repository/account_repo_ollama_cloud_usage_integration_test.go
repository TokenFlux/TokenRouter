//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListDueOllamaCloudUsageAccountsOrderingLimitAndProxyHydration(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	proxy := mustCreateProxy(t, tx.Client(), &service.Proxy{
		Name: "ollama-due-proxy", Protocol: "http", Host: "127.0.0.1", Port: 3128,
		Username: "user", Password: "pass", Status: service.StatusActive,
	})

	createAccount := func(name, baseURL string, proxyID *int64, snapshot map[string]any, lastUsed *time.Time) *service.Account {
		t.Helper()
		extra := map[string]any{
			service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
			service.OllamaCloudUsageAutoRefreshExtraKey: true,
		}
		if snapshot != nil {
			extra[service.OllamaCloudUsageSnapshotExtraKey] = snapshot
		}
		return mustCreateAccount(t, tx.Client(), &service.Account{
			Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": name, "base_url": baseURL},
			Extra:       extra, ProxyID: proxyID, LastUsedAt: lastUsed,
		})
	}

	uppercasePath := createAccount("ollama-uppercase-path", "https://ollama.com/V1", nil, nil, nil)
	missingSnapshot := createAccount("ollama-due-missing", "HTTPS://WWW.OLLAMA.COM:443/v1", &proxy.ID, nil, nil)
	fetched := now.Add(-2 * time.Hour)
	activity := now.Add(-5 * time.Minute)
	due := createAccount("ollama-due-activity", "https://ollama.com", nil, map[string]any{
		"status":          service.OllamaCloudUsageStatusOK,
		"fetched_at":      fetched.UTC().Format(time.RFC3339Nano),
		"last_attempt_at": fetched.UTC().Format(time.RFC3339Nano),
		"next_refresh_at": fetched.Add(time.Hour).UTC().Format(time.RFC3339Nano),
	}, &activity)
	// 成功快照之后没有新活动时，不应进入候选列表。
	_ = createAccount("ollama-not-due-idle", "https://ollama.com", nil, map[string]any{
		"status":          service.OllamaCloudUsageStatusOK,
		"fetched_at":      now.Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"last_attempt_at": now.Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"next_refresh_at": now.Add(-time.Minute).UTC().Format(time.RFC3339Nano),
	}, nil)
	_ = createAccount("ollama-ineligible", "https://ollama.com.evil.test", nil, nil, nil)

	accounts, err := repo.ListDueOllamaCloudUsageAccounts(ctx, now, time.Minute, time.Hour, 2)

	require.NoError(t, err)
	require.Len(t, accounts, 2)
	require.Equal(t, missingSnapshot.ID, accounts[0].ID)
	require.Equal(t, due.ID, accounts[1].ID)
	require.NotNil(t, accounts[1].LastUsedAt)
	require.WithinDuration(t, activity.UTC(), accounts[1].LastUsedAt.UTC(), time.Second)
	require.NotContains(t, accountIDs(accounts), uppercasePath.ID)
	require.NotNil(t, accounts[0].Proxy)
	require.Equal(t, proxy.ID, accounts[0].Proxy.ID)
	require.Equal(t, proxy.URL(), accounts[0].Proxy.URL())
}

// TestListDueOllamaCloudUsageAccountsParsesAllRFC3339Precisions 固定 SQL 时间戳解析路径，
// 覆盖实际写入数据库的小数秒精度和时区表示。
//
// 每个夹具的 fetched_at 都只早 2 分钟，并在 30 秒后产生活动；正确解析的记录尚未到期，
// 因为防抖与最小抓取间隔都会把到期时间推到未来。解析失败的时间戳会变为 NULL，
// 并进入开放分支而被判定到期，因此必须断言这些记录不存在，测试才能捕获解析失败：
//
//   - Go 使用 UTC 时间，即以 "Z" 标识。PostgreSQL 17 起 jsonpath .datetime() 才接受 "Z"，
//     因此若没有 ollamaCloudUsageParseRFC3339SQL 中的 Z -> +00:00 改写，
//     PostgreSQL 14–16 会把所有夹具误判为到期；
//   - 7/8/9 位小数秒超过 .datetime() 支持的微秒精度，必须先截断。
//
// 可在最低支持版本上运行，覆盖与数据库版本相关的路径：
//
//	TOKENROUTER_TEST_POSTGRES_IMAGE=postgres:15-alpine go test -tags integration ./internal/repository/
func TestListDueOllamaCloudUsageAccountsParsesAllRFC3339Precisions(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	now := time.Date(2026, time.July, 22, 14, 0, 0, 0, time.UTC)
	activity := now.Add(-30 * time.Second)

	// 三个值表示同一个 now-2m 时刻，但使用不同精度和时区表示。
	notDue := map[string]string{
		"nano-z":         "2026-07-22T13:58:00.123456789Z",
		"eight-positive": "2026-07-22T14:58:00.12345678+01:00",
		"seven-negative": "2026-07-22T11:58:00.1234567-02:00",
	}
	for name, fetchedAt := range notDue {
		_ = mustCreateAccount(t, tx.Client(), &service.Account{
			Name: "ollama-precision-" + name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": "precision-" + name, "base_url": "https://ollama.com"},
			Extra: map[string]any{
				service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
				service.OllamaCloudUsageAutoRefreshExtraKey: true,
				service.OllamaCloudUsageSnapshotExtraKey: map[string]any{
					"status":          service.OllamaCloudUsageStatusOK,
					"fetched_at":      fetchedAt,
					"last_attempt_at": fetchedAt,
				},
			},
			LastUsedAt: &activity,
		})
	}

	// 防止测试空通过：真正到期的记录仍必须返回。
	staleFetched := now.Add(-2 * time.Hour)
	due := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "ollama-precision-due", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "precision-due", "base_url": "https://ollama.com"},
		Extra: map[string]any{
			service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
			service.OllamaCloudUsageAutoRefreshExtraKey: true,
			service.OllamaCloudUsageSnapshotExtraKey: map[string]any{
				"status":          service.OllamaCloudUsageStatusOK,
				"fetched_at":      staleFetched.UTC().Format(time.RFC3339Nano),
				"last_attempt_at": staleFetched.UTC().Format(time.RFC3339Nano),
			},
		},
		LastUsedAt: &activity,
	})

	accounts, err := repo.ListDueOllamaCloudUsageAccounts(ctx, now, time.Minute, time.Hour, 10)

	require.NoError(t, err)
	ids := accountIDs(accounts)
	require.Contains(t, ids, due.ID, "a stale snapshot with fresh activity must be due")
	require.Len(t, ids, 1,
		"only the stale group may be due; extra rows mean a timestamp failed to parse and fell into the fail-open branch")
}

func TestListDueOllamaCloudUsageAccountsUsesGroupMaxLastUsedAndFailsOpen(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	now := time.Date(2026, time.July, 22, 14, 0, 0, 0, time.UTC)
	fetched := now.Add(-30 * time.Minute)
	older := now.Add(-10 * time.Minute)
	newer := now.Add(-2 * time.Minute)

	leader := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "ollama-group-leader", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "shared-key", "base_url": "https://ollama.com"},
		Extra: map[string]any{
			service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
			service.OllamaCloudUsageAutoRefreshExtraKey: true,
			service.OllamaCloudUsageSnapshotExtraKey: map[string]any{
				"status":          service.OllamaCloudUsageStatusOK,
				"fetched_at":      fetched.UTC().Format(time.RFC3339Nano),
				"last_attempt_at": fetched.UTC().Format(time.RFC3339Nano),
				"next_refresh_at": fetched.Add(time.Hour).UTC().Format(time.RFC3339Nano),
			},
		},
		LastUsedAt: &older,
	})
	_ = mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "ollama-group-sibling", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "shared-key", "base_url": "https://www.ollama.com/v1"},
		LastUsedAt:  &newer,
	})
	invalid := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "ollama-invalid-snapshot", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "invalid-key", "base_url": "https://ollama.com"},
		Extra: map[string]any{
			service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
			service.OllamaCloudUsageAutoRefreshExtraKey: true,
			service.OllamaCloudUsageSnapshotExtraKey: map[string]any{
				"status": service.OllamaCloudUsageStatusOK, "fetched_at": "2026-02-30T09:00:00.123456789Z",
			},
		},
	})
	idle := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "ollama-idle-ok", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "idle-key", "base_url": "https://ollama.com"},
		Extra: map[string]any{
			service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
			service.OllamaCloudUsageAutoRefreshExtraKey: true,
			service.OllamaCloudUsageSnapshotExtraKey: map[string]any{
				"status":          service.OllamaCloudUsageStatusOK,
				"fetched_at":      fetched.UTC().Format(time.RFC3339Nano),
				"last_attempt_at": fetched.UTC().Format(time.RFC3339Nano),
				"next_refresh_at": fetched.Add(time.Hour).UTC().Format(time.RFC3339Nano),
			},
		},
	})

	accounts, err := repo.ListDueOllamaCloudUsageAccounts(ctx, now, time.Minute, time.Hour, 10)

	require.NoError(t, err, "invalid stored values must not abort the query")
	ids := accountIDs(accounts)
	require.Contains(t, ids, invalid.ID)
	require.Contains(t, ids, leader.ID)
	require.NotContains(t, ids, idle.ID)
	for _, account := range accounts {
		if account.ID == leader.ID {
			require.NotNil(t, account.LastUsedAt)
			require.WithinDuration(t, newer.UTC(), account.LastUsedAt.UTC(), time.Second,
				"group MAX(last_used_at) must come from the sibling")
		}
	}
}

func TestLockAndMergeAccountManagedExtraCoalescesNullableOllamaGroupIdentity(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	account := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "ordinary-openai-without-base-url", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-no-base-url"},
		Extra:       map[string]any{"custom": true},
	})
	loaded, err := newAccountRepositoryWithSQL(tx.Client(), tx, nil).GetByID(ctx, account.ID)
	require.NoError(t, err)

	merged, err := lockAndMergeAccountManagedExtra(ctx, tx.Client(), loaded)

	require.NoError(t, err, "a NULL Ollama eligibility expression must scan as false")
	require.NotContains(t, merged, service.OllamaCloudUsageSessionExtraKey)
	require.Equal(t, true, merged["custom"])
}

func TestOllamaCloudUsageGroupWritesAreAtomicAcrossPlatformsAndURLVariants(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	create := func(name, platform, apiKey, baseURL string) *service.Account {
		t.Helper()
		return mustCreateAccount(t, tx.Client(), &service.Account{
			Name: name, Platform: platform, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": apiKey, "base_url": baseURL},
			Extra:       map[string]any{},
		})
	}
	first := create("ollama-group-openai", service.PlatformOpenAI, "shared-key", "https://ollama.com")
	second := create("ollama-group-anthropic", service.PlatformAnthropic, "shared-key", "HTTPS://WWW.OLLAMA.COM:443/v1")
	different := create("ollama-group-different", service.PlatformOpenAI, "different-key", "https://ollama.com")

	require.NoError(t, repo.SaveOllamaCloudUsageSession(ctx, first, "cipher:shared", false))
	for _, id := range []int64{first.ID, second.ID} {
		account, err := repo.GetByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, "cipher:shared", account.Extra[service.OllamaCloudUsageSessionExtraKey])
		require.Equal(t, false, account.Extra[service.OllamaCloudUsageAutoRefreshExtraKey])
	}
	differentLoaded, err := repo.GetByID(ctx, different.ID)
	require.NoError(t, err)
	require.NotContains(t, differentLoaded.Extra, service.OllamaCloudUsageSessionExtraKey)

	secondLoaded, err := repo.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.NoError(t, repo.SetOllamaCloudUsageAutoRefresh(ctx, secondLoaded, true))
	firstLoaded, err := repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	secondLoaded, err = repo.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, true, firstLoaded.Extra[service.OllamaCloudUsageAutoRefreshExtraKey])
	require.Equal(t, true, secondLoaded.Extra[service.OllamaCloudUsageAutoRefreshExtraKey])

	now := time.Now().UTC()
	snapshot := &service.OllamaCloudUsageSnapshot{
		Status: service.OllamaCloudUsageStatusOK, LastAttemptAt: now, NextRefreshAt: now.Add(time.Hour),
	}
	require.NoError(t, repo.UpdateOllamaCloudUsageSnapshot(ctx, firstLoaded, snapshot))
	secondLoaded, err = repo.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, service.OllamaCloudUsageStatusOK,
		secondLoaded.Extra[service.OllamaCloudUsageSnapshotExtraKey].(map[string]any)["status"])

	staleSecond := secondLoaded
	require.NoError(t, repo.UpdateCredentials(ctx, second.ID, map[string]any{
		"api_key": "rotated-key", "base_url": "https://ollama.com",
	}))
	require.ErrorIs(t, repo.DisableOllamaCloudUsageAutoRefresh(ctx, staleSecond), service.ErrOllamaCloudUsageIdentityChanged)
	firstLoaded, err = repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	secondLoaded, err = repo.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, "cipher:shared", firstLoaded.Extra[service.OllamaCloudUsageSessionExtraKey])
	require.Equal(t, true, firstLoaded.Extra[service.OllamaCloudUsageAutoRefreshExtraKey])
	require.NotContains(t, secondLoaded.Extra, service.OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, secondLoaded.Extra, service.OllamaCloudUsageAutoRefreshExtraKey)

	require.NoError(t, repo.DeleteOllamaCloudUsageSession(ctx, firstLoaded))
	firstLoaded, err = repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.NotContains(t, firstLoaded.Extra, service.OllamaCloudUsageSessionExtraKey)
}

func TestConcurrentOllamaCloudUsageSaveAndDeleteSerializeGroupState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	suffix := time.Now().UnixNano()
	apiKey := fmt.Sprintf("ollama-concurrent-%d", suffix)
	create := func(platform string) *service.Account {
		t.Helper()
		return mustCreateAccount(t, client, &service.Account{
			Name: fmt.Sprintf("%s-%s", apiKey, platform), Platform: platform, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": apiKey, "base_url": "https://ollama.com"},
			Extra: map[string]any{
				service.OllamaCloudUsageSessionExtraKey:     "cipher:initial",
				service.OllamaCloudUsageAutoRefreshExtraKey: true,
			},
		})
	}
	first := create(service.PlatformOpenAI)
	second := create(service.PlatformAnthropic)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id IN ($1, $2)", first.ID, second.ID)
	})
	anchor, err := repo.GetByID(ctx, first.ID)
	require.NoError(t, err)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- repo.SaveOllamaCloudUsageSession(ctx, anchor, "cipher:replacement", true)
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- repo.DeleteOllamaCloudUsageSession(ctx, anchor)
	}()
	close(start)
	wg.Wait()
	close(errs)
	for writeErr := range errs {
		require.NoError(t, writeErr)
	}

	firstLoaded, err := repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	secondLoaded, err := repo.GetByID(ctx, second.ID)
	require.NoError(t, err)
	managedState := func(account *service.Account) map[string]any {
		state := make(map[string]any)
		for _, key := range []string{
			service.OllamaCloudUsageSessionExtraKey,
			service.OllamaCloudUsageAutoRefreshExtraKey,
			service.OllamaCloudUsageSnapshotExtraKey,
		} {
			if value, ok := account.Extra[key]; ok {
				state[key] = value
			}
		}
		return state
	}
	firstState := managedState(firstLoaded)
	require.Equal(t, firstState, managedState(secondLoaded), "a serialized last commit must own the whole group")
	if len(firstState) > 0 {
		require.Equal(t, "cipher:replacement", firstState[service.OllamaCloudUsageSessionExtraKey])
		require.Equal(t, true, firstState[service.OllamaCloudUsageAutoRefreshExtraKey])
		require.NotContains(t, firstState, service.OllamaCloudUsageSnapshotExtraKey)
	}
}

func accountIDs(accounts []service.Account) []int64 {
	ids := make([]int64, len(accounts))
	for index := range accounts {
		ids[index] = accounts[index].ID
	}
	return ids
}

func TestOllamaCloudUsageCredentialAndBulkUpdatesPreserveManagedStateOnlyWhenSafe(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	now := time.Now().UTC()
	newAccount := func(name string) *service.Account {
		return mustCreateAccount(t, tx.Client(), &service.Account{
			Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": "old-key", "base_url": "https://ollama.com"},
			Extra: map[string]any{
				service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
				service.OllamaCloudUsageAutoRefreshExtraKey: true,
				service.OllamaCloudUsageSnapshotExtraKey: map[string]any{
					"status": service.OllamaCloudUsageStatusOK, "last_attempt_at": now, "next_refresh_at": now.Add(time.Hour),
				},
			},
		})
	}

	rawAccount := newAccount("ollama-raw-credentials")
	require.NoError(t, repo.UpdateCredentials(ctx, rawAccount.ID, map[string]any{
		"api_key": "old-key", "base_url": "https://ollama.com/V1",
	}))
	rawUpdated, err := repo.GetByID(ctx, rawAccount.ID)
	require.NoError(t, err)
	require.NotContains(t, rawUpdated.Extra, service.OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, rawUpdated.Extra, service.OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, rawUpdated.Extra, service.OllamaCloudUsageSnapshotExtraKey)

	bulkAccount := newAccount("ollama-bulk-credentials")
	rows, err := repo.BulkUpdate(ctx, []int64{bulkAccount.ID}, service.AccountBulkUpdate{
		Credentials: map[string]any{"base_url": "HTTPS://WWW.OLLAMA.COM:443/v1"},
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)
	bulkUnchanged, err := repo.GetByID(ctx, bulkAccount.ID)
	require.NoError(t, err)
	require.Contains(t, bulkUnchanged.Extra, service.OllamaCloudUsageSnapshotExtraKey)

	rows, err = repo.BulkUpdate(ctx, []int64{bulkAccount.ID}, service.AccountBulkUpdate{
		Credentials: map[string]any{"base_url": "https://ollama.com/V1"},
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)
	bulkIneligible, err := repo.GetByID(ctx, bulkAccount.ID)
	require.NoError(t, err)
	require.NotContains(t, bulkIneligible.Extra, service.OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, bulkIneligible.Extra, service.OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, bulkIneligible.Extra, service.OllamaCloudUsageSnapshotExtraKey)
}

func TestProxyIdentityUpdateInvalidatesOllamaSnapshotAndRejectsInFlightCAS(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	accountRepo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	proxyRepo := newProxyRepositoryWithSQL(tx.Client(), tx)
	proxy := mustCreateProxy(t, tx.Client(), &service.Proxy{
		Name: "ollama-identity-proxy", Protocol: "http", Host: "old.example", Port: 8080,
		Username: "old-user", Password: "old-pass", Status: service.StatusActive,
	})
	now := time.Now().UTC()
	account := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "ollama-proxy-account", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "key", "base_url": "https://ollama.com"},
		ProxyID:     &proxy.ID,
		Extra: map[string]any{
			service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
			service.OllamaCloudUsageAutoRefreshExtraKey: true,
			service.OllamaCloudUsageSnapshotExtraKey: map[string]any{
				"status": service.OllamaCloudUsageStatusOK, "last_attempt_at": now, "next_refresh_at": now.Add(time.Hour),
			},
		},
	})
	inFlight, err := accountRepo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.NotNil(t, inFlight.Proxy)
	require.Equal(t, "old.example", inFlight.Proxy.Host)

	proxyToUpdate, err := proxyRepo.GetByID(ctx, proxy.ID)
	require.NoError(t, err)
	proxyToUpdate.Host = "new.example"
	require.NoError(t, proxyRepo.Update(ctx, proxyToUpdate))

	got, err := accountRepo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.NotContains(t, got.Extra, service.OllamaCloudUsageSnapshotExtraKey)
	require.Equal(t, "cipher:wos-session=fixture", got.Extra[service.OllamaCloudUsageSessionExtraKey])
	require.Equal(t, true, got.Extra[service.OllamaCloudUsageAutoRefreshExtraKey])

	err = accountRepo.UpdateOllamaCloudUsageSnapshot(ctx, inFlight, &service.OllamaCloudUsageSnapshot{
		Status: service.OllamaCloudUsageStatusOK, LastAttemptAt: now, NextRefreshAt: now.Add(time.Hour),
	})
	require.ErrorIs(t, err, service.ErrOllamaCloudUsageIdentityChanged)
}

// 无变化的凭证持久化仍需清除废弃字段，但不得影响 Ollama 状态；真实凭据变化会使其失效。
func TestUpdateCredentialsPreservesOllamaAndDiscardsDeprecatedExtra(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)

	legacyAccount := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "openai-legacy-extra", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-legacy", "base_url": "https://relay.example.com/v1"},
		Extra: map[string]any{
			deprecatedUpstreamBillingProbeEnabledExtraKey: true,
			deprecatedUpstreamBillingProbeExtraKey:        map[string]any{"status": "ok"},
			"custom":                                      "value",
		},
	})
	require.NoError(t, repo.UpdateCredentials(ctx, legacyAccount.ID, map[string]any{
		"api_key": "sk-legacy", "base_url": "https://relay.example.com/v1",
	}))
	legacyLoaded, err := repo.GetByID(ctx, legacyAccount.ID)
	require.NoError(t, err)
	require.NotContains(t, legacyLoaded.Extra, deprecatedUpstreamBillingProbeExtraKey)
	require.NotContains(t, legacyLoaded.Extra, deprecatedUpstreamBillingProbeEnabledExtraKey)
	require.Equal(t, "value", legacyLoaded.Extra["custom"])

	now := time.Now().UTC()
	ollamaAccount := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "ollama-unchanged", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "ollama-key", "base_url": "https://ollama.com"},
		Extra: map[string]any{
			service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
			service.OllamaCloudUsageAutoRefreshExtraKey: true,
			service.OllamaCloudUsageSnapshotExtraKey: map[string]any{
				"status": service.OllamaCloudUsageStatusOK, "last_attempt_at": now, "next_refresh_at": now.Add(time.Hour),
			},
		},
	})
	require.NoError(t, repo.UpdateCredentials(ctx, ollamaAccount.ID, map[string]any{
		"api_key": "ollama-key", "base_url": "https://ollama.com",
	}))
	ollamaLoaded, err := repo.GetByID(ctx, ollamaAccount.ID)
	require.NoError(t, err)
	require.Equal(t, "cipher:wos-session=fixture", ollamaLoaded.Extra[service.OllamaCloudUsageSessionExtraKey])
	require.Equal(t, true, ollamaLoaded.Extra[service.OllamaCloudUsageAutoRefreshExtraKey])
	require.Contains(t, ollamaLoaded.Extra, service.OllamaCloudUsageSnapshotExtraKey)

	require.NoError(t, repo.UpdateCredentials(ctx, ollamaAccount.ID, map[string]any{
		"api_key": "ollama-key-rotated", "base_url": "https://ollama.com",
	}))
	ollamaLoaded, err = repo.GetByID(ctx, ollamaAccount.ID)
	require.NoError(t, err)
	require.NotContains(t, ollamaLoaded.Extra, service.OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, ollamaLoaded.Extra, service.OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, ollamaLoaded.Extra, service.OllamaCloudUsageSnapshotExtraKey)
}

// TestListDueOllamaCloudUsageAccountsSQLDueRulesMatchService 验证 SQL 候选层会在 LIMIT 前
// 应用防抖、最大等待和失败退避规则，并与 service.ollamaCloudUsageIsAutoRefreshDue 保持一致；
// 同时验证超过 20 个活跃但未到期的分组不会让真正达到最大等待时间的分组饥饿。
func TestListDueOllamaCloudUsageAccountsSQLDueRulesMatchService(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	debounce := time.Minute
	maxWait := time.Hour

	createOK := func(name string, fetched, lastUsed time.Time) *service.Account {
		t.Helper()
		return mustCreateAccount(t, tx.Client(), &service.Account{
			Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": name, "base_url": "https://ollama.com"},
			Extra: map[string]any{
				service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
				service.OllamaCloudUsageAutoRefreshExtraKey: true,
				service.OllamaCloudUsageSnapshotExtraKey: map[string]any{
					"status":          service.OllamaCloudUsageStatusOK,
					"fetched_at":      fetched.UTC().Format(time.RFC3339Nano),
					"last_attempt_at": fetched.UTC().Format(time.RFC3339Nano),
					"next_refresh_at": fetched.Add(maxWait).UTC().Format(time.RFC3339Nano),
				},
			},
			LastUsedAt: &lastUsed,
		})
	}
	createFailed := func(name string, lastAttempt, lastUsed, nextRefresh time.Time, nextRefreshRaw string) *service.Account {
		t.Helper()
		snapshot := map[string]any{
			"status":          service.OllamaCloudUsageStatusFailed,
			"last_attempt_at": lastAttempt.UTC().Format(time.RFC3339Nano),
			"failure_count":   1,
		}
		if nextRefreshRaw != "" {
			snapshot["next_refresh_at"] = nextRefreshRaw
		} else {
			snapshot["next_refresh_at"] = nextRefresh.UTC().Format(time.RFC3339Nano)
		}
		return mustCreateAccount(t, tx.Client(), &service.Account{
			Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": name, "base_url": "https://ollama.com"},
			Extra: map[string]any{
				service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
				service.OllamaCloudUsageAutoRefreshExtraKey: true,
				service.OllamaCloudUsageSnapshotExtraKey:    snapshot,
			},
			LastUsedAt: &lastUsed,
		})
	}

	// 21 个分组在刷新后有活动但尚未经过防抖期；旧实现中它们会每分钟占满 20 个名额，
	// 使真正到期的分组无法刷新。
	notDueIDs := make(map[int64]struct{}, 21)
	for i := 0; i < 21; i++ {
		// 10 分钟前刷新、10 秒前使用，due_at = lastUsed+debounce = now+50s，尚未到期。
		acc := createOK(fmt.Sprintf("ollama-not-due-debounce-%02d", i), now.Add(-10*time.Minute), now.Add(-10*time.Second))
		notDueIDs[acc.ID] = struct{}{}
	}

	// 通过最大等待规则真正到期：2 小时前刷新，持续活动到 10 秒前。
	// due_at = min(now-10s+1m, now-2h+1h) = now-1h，已经到期。
	maxWaitDue := createOK("ollama-due-maxwait", now.Add(-2*time.Hour), now.Add(-10*time.Second))

	// 成功后防抖期已过：2 分钟前使用且防抖为 1 分钟，应当到期。
	debounceDue := createOK("ollama-due-debounce", now.Add(-30*time.Minute), now.Add(-2*time.Minute))

	// 成功后仍在防抖期内，不应到期。
	_ = createOK("ollama-not-due-fresh", now.Add(-30*time.Minute), now.Add(-20*time.Second))

	// 失败后即使有新活动，也会受 next_refresh_at 退避约束。
	_ = createFailed("ollama-fail-backoff", now.Add(-30*time.Minute), now.Add(-2*time.Minute), now.Add(10*time.Minute), "")

	// 失败退避结束后出现新请求，应当到期。
	failDue := createFailed("ollama-fail-due", now.Add(-30*time.Minute), now.Add(-2*time.Minute), now.Add(-time.Minute), "")

	// next_refresh_at 无效时按开放策略处理，既不终止查询，也不阻止活动到期。
	failInvalidNext := createFailed("ollama-fail-invalid-next", now.Add(-30*time.Minute), now.Add(-2*time.Minute), time.Time{}, "not-a-timestamp")

	accounts, err := repo.ListDueOllamaCloudUsageAccounts(ctx, now, debounce, maxWait, 20)
	require.NoError(t, err)

	ids := accountIDs(accounts)
	require.Contains(t, ids, maxWaitDue.ID, "max-wait due group must not be starved by not-yet-due activity groups")
	require.Contains(t, ids, debounceDue.ID, "success debounce elapsed must be due in SQL")
	require.Contains(t, ids, failDue.ID, "failure after backoff with new activity must be due in SQL")
	require.Contains(t, ids, failInvalidNext.ID, "invalid next_refresh_at must fail open to activity due")
	require.LessOrEqual(t, len(accounts), 20)

	// 以下夹具与 service.ollamaCloudUsageIsAutoRefreshDue 的语义一致；
	// 即使未到期分组数量超过上限，它们也不应出现在结果中。
	for _, id := range ids {
		_, isNotDue := notDueIDs[id]
		require.False(t, isNotDue, "not-yet-due debounce group %d must not be returned by SQL LIMIT layer", id)
	}
	require.NotContains(t, ids, int64(0))

	// 明确未到期的名称必须被排除，包括刚成功刷新的分组和仍在退避期的失败分组。
	for _, account := range accounts {
		require.NotContains(t, account.Name, "not-due")
		require.NotEqual(t, "ollama-fail-backoff", account.Name)
		require.NotEqual(t, "ollama-not-due-fresh", account.Name)
	}
}
