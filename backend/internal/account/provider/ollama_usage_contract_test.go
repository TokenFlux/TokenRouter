package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	upstreamollama "github.com/TokenFlux/TokenRouter/internal/upstream/ollama"
	"github.com/stretchr/testify/require"
)

type ollamaUsageTestEncryptor struct{}

func (ollamaUsageTestEncryptor) Encrypt(value string) (string, error) { return "cipher:" + value, nil }
func (ollamaUsageTestEncryptor) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "cipher:") {
		return "", errors.New("authentication failed")
	}
	return strings.TrimPrefix(value, "cipher:"), nil
}

type ollamaUsageTestRepo struct {
	*ollamaUsageRows
	due                 []accountcore.Record
	beforeSnapshot      func()
	disableAutoAttempts atomic.Int64
	disableAutoCalls    atomic.Int64
	groupResolveCalls   atomic.Int64
	getByIDCalls        atomic.Int64
}

// GetByID 记录加载次数，让并发测试等待调用方到达 singleflight 前的确定位置。
func (r *ollamaUsageTestRepo) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	r.getByIDCalls.Add(1)
	return r.ollamaUsageRows.GetByID(ctx, id)
}

func (r *ollamaUsageTestRepo) ListOllamaCloudUsageGroupAccounts(_ context.Context, anchors []*accountcore.Record) ([]accountcore.Record, error) {
	r.groupResolveCalls.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	wanted := make(map[string]struct{}, len(anchors))
	for _, anchor := range anchors {
		if fingerprint, ok := accountcore.OllamaCloudUsageGroupFingerprint(anchor); ok {
			wanted[fingerprint] = struct{}{}
		}
	}
	result := make([]accountcore.Record, 0, len(r.accounts))
	for _, account := range r.accounts {
		fingerprint, ok := accountcore.OllamaCloudUsageGroupFingerprint(account)
		if _, match := wanted[fingerprint]; !ok || !match {
			continue
		}
		result = append(result, cloneOllamaUsageTestAccount(*account))
	}
	return result, nil
}

// cloneOllamaUsageTestAccount 深拷贝共享 map，模拟真实仓储每次查询返回全新行：
// 组写在 r.mu 下改成员 map，浅拷贝会让 RunDue 过滤循环无锁读到同一 map 而竞争。
func cloneOllamaUsageTestAccount(account accountcore.Record) accountcore.Record {
	account.Credentials = accountcore.CRSMergeMap(nil, account.Credentials)
	account.Extra = accountcore.CRSMergeMap(nil, account.Extra)
	return account
}

func (r *ollamaUsageTestRepo) SaveOllamaCloudUsageSession(_ context.Context, expected *accountcore.Record, ciphertext string, autoRefresh bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	members, err := r.ollamaGroupMembersLocked(expected)
	if err != nil {
		return err
	}
	for _, account := range members {
		account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = ciphertext
		account.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = autoRefresh
		delete(account.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
	}
	return nil
}

func (r *ollamaUsageTestRepo) DeleteOllamaCloudUsageSession(_ context.Context, expected *accountcore.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	members, err := r.ollamaGroupMembersLocked(expected)
	if err != nil {
		return err
	}
	for _, account := range members {
		delete(account.Extra, accountcore.OllamaCloudUsageSessionExtraKey)
		delete(account.Extra, accountcore.OllamaCloudUsageAutoRefreshExtraKey)
		delete(account.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
	}
	return nil
}

func (r *ollamaUsageTestRepo) SetOllamaCloudUsageAutoRefresh(_ context.Context, expected *accountcore.Record, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	members, err := r.ollamaGroupMembersLocked(expected)
	if err != nil || !r.ollamaExpectedSessionExistsLocked(members, expected) {
		return accountcore.ErrOllamaCloudUsageIdentityChanged
	}
	for _, account := range members {
		applyOllamaUsageTestManagedExtra(account, expected)
		account.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = enabled
	}
	return nil
}

func (r *ollamaUsageTestRepo) UpdateOllamaCloudUsageSnapshot(_ context.Context, expected *accountcore.Record, snapshot *accountcore.OllamaCloudUsageSnapshot) error {
	if r.beforeSnapshot != nil {
		r.beforeSnapshot()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	members, err := r.ollamaGroupMembersLocked(expected)
	if err != nil || !r.ollamaExpectedSessionExistsLocked(members, expected) {
		return accountcore.ErrOllamaCloudUsageIdentityChanged
	}
	for _, account := range members {
		applyOllamaUsageTestManagedExtra(account, expected)
		account.Extra[accountcore.OllamaCloudUsageSnapshotExtraKey] = snapshot
	}
	return nil
}

func (r *ollamaUsageTestRepo) DisableOllamaCloudUsageAutoRefresh(_ context.Context, expected *accountcore.Record) error {
	r.disableAutoAttempts.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	members, err := r.ollamaGroupMembersLocked(expected)
	if err != nil || !r.ollamaExpectedSessionExistsLocked(members, expected) {
		return accountcore.ErrOllamaCloudUsageIdentityChanged
	}
	for _, account := range members {
		applyOllamaUsageTestManagedExtra(account, expected)
		account.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = false
		delete(account.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
	}
	r.disableAutoCalls.Add(1)
	return nil
}

func (r *ollamaUsageTestRepo) ollamaGroupMembersLocked(expected *accountcore.Record) ([]*accountcore.Record, error) {
	anchor := r.accounts[expected.ID]
	if !sameOllamaUsageTestIdentity(anchor, expected) {
		return nil, accountcore.ErrOllamaCloudUsageIdentityChanged
	}
	fingerprint, ok := accountcore.OllamaCloudUsageGroupFingerprint(expected)
	if !ok {
		return nil, accountcore.ErrOllamaCloudUsageAccountInvalid
	}
	members := make([]*accountcore.Record, 0, len(r.accounts))
	for _, account := range r.accounts {
		candidate, valid := accountcore.OllamaCloudUsageGroupFingerprint(account)
		if valid && candidate == fingerprint {
			if account.Extra == nil {
				account.Extra = make(map[string]any)
			}
			members = append(members, account)
		}
	}
	return members, nil
}

func (r *ollamaUsageTestRepo) ollamaExpectedSessionExistsLocked(members []*accountcore.Record, expected *accountcore.Record) bool {
	for _, member := range members {
		if member.Extra[accountcore.OllamaCloudUsageSessionExtraKey] == expected.Extra[accountcore.OllamaCloudUsageSessionExtraKey] {
			return true
		}
	}
	return false
}

func applyOllamaUsageTestManagedExtra(account, source *accountcore.Record) {
	for _, key := range []string{accountcore.OllamaCloudUsageSessionExtraKey, accountcore.OllamaCloudUsageAutoRefreshExtraKey, accountcore.OllamaCloudUsageSnapshotExtraKey} {
		delete(account.Extra, key)
		if value, ok := source.Extra[key]; ok {
			account.Extra[key] = value
		}
	}
}

func (r *ollamaUsageTestRepo) ListDueOllamaCloudUsageAccounts(_ context.Context, _ time.Time, _, _ time.Duration, limit int) ([]accountcore.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.due) > 0 {
		out := make([]accountcore.Record, 0, min(limit, len(r.due)))
		for _, account := range r.due[:min(limit, len(r.due))] {
			out = append(out, cloneOllamaUsageTestAccount(account))
		}
		return out, nil
	}
	out := make([]accountcore.Record, 0, len(r.accounts))
	for _, account := range r.accounts {
		out = append(out, cloneOllamaUsageTestAccount(*account))
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

type ollamaRefreshPreflightIdentityChangeRepo struct {
	*ollamaUsageTestRepo
	getCalls atomic.Int64
}

func (r *ollamaRefreshPreflightIdentityChangeRepo) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	if r.getCalls.Add(1) == 2 {
		r.mu.Lock()
		r.accounts[id].Credentials["api_key"] = "rotated-before-refresh"
		r.mu.Unlock()
	}
	return r.ollamaUsageRows.GetByID(ctx, id)
}

func sameOllamaUsageTestIdentity(left, right *accountcore.Record) bool {
	return left != nil && right != nil && left.Platform == right.Platform && left.Type == right.Type &&
		reflect.DeepEqual(left.Credentials, right.Credentials) && reflect.DeepEqual(left.ProxyID, right.ProxyID)
}

type ollamaUsageHTTPStub struct {
	status         int
	body           []byte
	header         http.Header
	calls          atomic.Int64
	active         atomic.Int64
	maxActive      atomic.Int64
	beforeResponse func(*http.Request)
	lastRequest    *http.Request
	lastProxyURL   string
	mu             sync.Mutex
}

func (s *ollamaUsageHTTPStub) Do(req *http.Request, proxyURL string, _ int64, _ int) (*http.Response, error) {
	s.calls.Add(1)
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		peak := s.maxActive.Load()
		if active <= peak || s.maxActive.CompareAndSwap(peak, active) {
			break
		}
	}
	s.mu.Lock()
	s.lastRequest = req
	s.lastProxyURL = proxyURL
	s.mu.Unlock()
	if s.beforeResponse != nil {
		s.beforeResponse(req)
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	header := s.header
	if header == nil {
		header = http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(string(s.body))), Request: req}, nil
}

func (s *ollamaUsageHTTPStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, concurrency)
}

func ollamaUsageAccount(id int64) *accountcore.Record {
	return &accountcore.Record{
		ID: id, Name: fmt.Sprintf("ollama-%d", id), Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://ollama.com", "api_key": fmt.Sprintf("key-%d", id)},
		Extra:       map[string]any{}, Status: accountcore.StatusActive, Schedulable: true, Concurrency: 1,
	}
}

func newOllamaUsageTestService(t *testing.T, repo *ollamaUsageTestRepo, upstream ollamaUsageTransport, settingsRepo settingscore.Repository, fixedKey bool) *ollamaUsageContract {
	t.Helper()
	svc := newOllamaUsageContract(repo, upstream, settingsRepo, ollamaUsageTestEncryptor{}, fixedKey)
	t.Cleanup(svc.Stop)
	return svc
}

func ollamaUsageFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("../../upstream/ollama/testdata/ollama_settings_usage.html")
	require.NoError(t, err)
	return body
}

func TestOllamaCloudUsageIsAutoRefreshDue(t *testing.T) {
	debounce := time.Minute
	maxWait := time.Hour
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	fetched := now.Add(-30 * time.Minute)
	ptr := func(ts time.Time) *time.Time { return &ts }

	require.True(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(nil, nil, now, debounce, maxWait), "missing snapshot first due")
	require.True(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(&accountcore.OllamaCloudUsageSnapshot{Status: "bogus"}, nil, now, debounce, maxWait), "invalid status first due")

	okSnap := &accountcore.OllamaCloudUsageSnapshot{
		Status: accountcore.OllamaCloudUsageStatusOK, FetchedAt: ptr(fetched),
		LastAttemptAt: fetched, NextRefreshAt: fetched.Add(maxWait),
	}
	require.False(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(okSnap, nil, now, debounce, maxWait), "no request after success")
	require.False(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(okSnap, ptr(fetched), now, debounce, maxWait), "request not after fetched_at")
	require.False(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(okSnap, ptr(now.Add(-30*time.Second)), now, debounce, maxWait), "debounce not elapsed")
	require.True(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(okSnap, ptr(now.Add(-time.Minute)), now, debounce, maxWait), "single request quiet for debounce")

	// 请求持续到达时，即使刚刚使用，旧刷新时间对应的最大等待也会强制到期。
	oldFetched := now.Add(-2 * time.Hour)
	oldSnap := &accountcore.OllamaCloudUsageSnapshot{
		Status: accountcore.OllamaCloudUsageStatusOK, FetchedAt: ptr(oldFetched),
		LastAttemptAt: oldFetched, NextRefreshAt: oldFetched.Add(maxWait),
	}
	require.True(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(oldSnap, ptr(now), now, debounce, maxWait), "max-wait forces due while requests continue")
	// 快照很旧时，首次请求因 fetched+maxWait 已过而立即到期。
	require.True(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(oldSnap, ptr(now.Add(-time.Second)), now, debounce, maxWait), "stale snapshot first request immediate")

	failSnap := &accountcore.OllamaCloudUsageSnapshot{
		Status: accountcore.OllamaCloudUsageStatusFailed, FetchedAt: ptr(fetched),
		LastAttemptAt: now.Add(-10 * time.Minute), NextRefreshAt: now.Add(20 * time.Minute),
	}
	require.False(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(failSnap, nil, now, debounce, maxWait), "failure without new request")
	require.False(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(failSnap, ptr(now.Add(-time.Minute)), now, debounce, maxWait), "failure blocked by backoff")
	failSnap.NextRefreshAt = now.Add(-time.Second)
	require.True(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(failSnap, ptr(now.Add(-time.Minute)), now, debounce, maxWait), "failure after backoff with new request")

	require.True(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(&accountcore.OllamaCloudUsageSnapshot{
		Status: accountcore.OllamaCloudUsageStatusOK, LastAttemptAt: now,
	}, nil, now, debounce, maxWait), "ok without fetched_at fails open")
}

// 成功路径已不再读取 next_refresh_at，而 nextOllamaCloudUsageDelay 原本通过该字段
// 应用最小间隔。活动只能把刷新提前到该下限，否则间隔略大于防抖期的请求流量会让
// 分组上游抓取频率远高于既有下限。
func TestOllamaCloudUsageAutoRefreshDueAtHonoursMinFetchInterval(t *testing.T) {
	debounce := time.Minute
	maxWait := time.Hour
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	ptr := func(ts time.Time) *time.Time { return &ts }

	// 防抖期已经结束，但最近一次成功抓取仍在最小间隔内。
	recent := now.Add(-5 * time.Minute)
	recentSnap := &accountcore.OllamaCloudUsageSnapshot{
		Status: accountcore.OllamaCloudUsageStatusOK, FetchedAt: ptr(recent), LastAttemptAt: recent,
	}
	dueAt, ok := accountcore.OllamaCloudUsageAutoRefreshDueAt(recentSnap, ptr(now.Add(-2*time.Minute)), debounce, maxWait)
	require.True(t, ok)
	require.Equal(t, recent.Add(accountcore.OllamaCloudUsageMinFetchInterval), dueAt,
		"due time must be clamped to fetched_at + min fetch interval")
	require.False(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(recentSnap, ptr(now.Add(-2*time.Minute)), now, debounce, maxWait),
		"debounce alone must not refresh within the min fetch interval")

	// 越过最小间隔后，再次由防抖时间决定是否到期。
	atFloor := now.Add(-accountcore.OllamaCloudUsageMinFetchInterval)
	floorSnap := &accountcore.OllamaCloudUsageSnapshot{
		Status: accountcore.OllamaCloudUsageStatusOK, FetchedAt: ptr(atFloor), LastAttemptAt: atFloor,
	}
	require.True(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(floorSnap, ptr(now.Add(-2*time.Minute)), now, debounce, maxWait),
		"past the floor a quiet debounce window is due")

	// 最小间隔不会推迟已经由最大等待强制触发的刷新。
	stale := now.Add(-2 * time.Hour)
	staleSnap := &accountcore.OllamaCloudUsageSnapshot{
		Status: accountcore.OllamaCloudUsageStatusOK, FetchedAt: ptr(stale), LastAttemptAt: stale,
	}
	require.True(t, accountcore.OllamaCloudUsageIsAutoRefreshDue(staleSnap, ptr(now), now, debounce, maxWait),
		"max-wait still forces due on a stale snapshot")
}

func TestIsOllamaCloudUsageAccountStrictOfficialHost(t *testing.T) {
	tests := []struct {
		baseURL  string
		platform string
		want     bool
	}{
		{"https://ollama.com", capability.PlatformOpenAI, true},
		{"HTTPS://OLLAMA.COM", capability.PlatformAnthropic, true},
		{"https://www.OLLAMA.com:443/v1", capability.PlatformOpenAI, true},
		{"https://ollama.com:443", capability.PlatformOpenAI, true},
		{"https://ollama.com/", capability.PlatformAnthropic, false},
		{"https://ollama.com/v1/", capability.PlatformOpenAI, false},
		{"http://ollama.com", capability.PlatformOpenAI, false},
		{"https://ollama.com.evil.test", capability.PlatformOpenAI, false},
		{"https://ollama.com:444", capability.PlatformOpenAI, false},
		{"https://user@ollama.com", capability.PlatformOpenAI, false},
		{"https://ollama.com/v2", capability.PlatformOpenAI, false},
		{"https://ollama.com?next=https://evil.test", capability.PlatformOpenAI, false},
		{"https://ollama.com#usage", capability.PlatformOpenAI, false},
	}
	for _, test := range tests {
		t.Run(test.baseURL+test.platform, func(t *testing.T) {
			account := ollamaUsageAccount(1)
			account.Platform = test.platform
			account.Credentials["base_url"] = test.baseURL
			require.Equal(t, test.want, accountcore.IsOllamaCloudUsageAccount(account))
		})
	}
}

func TestNormalizeOllamaCloudUsageCookieAllowlist(t *testing.T) {
	normalized, err := egress.NormalizeOllamaCloudUsageCookie(" tracking=discard ; wos-session=secret ; __Secure-authjs.session-token.0=part-a ; device=discard ")
	require.NoError(t, err)
	require.Equal(t, "wos-session=secret; __Secure-authjs.session-token.0=part-a", normalized)

	normalized, err = egress.NormalizeOllamaCloudUsageCookie(" \t\r\nwos-session=secret; tracking=discard\r\n\t ")
	require.NoError(t, err)
	require.Equal(t, "wos-session=secret", normalized)

	_, err = egress.NormalizeOllamaCloudUsageCookie("wos-session=secret\r\nHost: evil.test")
	require.ErrorContains(t, err, "invalid header")

	for _, allowed := range []string{
		"wos-session", "__Secure-session", "session", "ollama_session", "__Host-ollama_session",
		"next-auth.session-token", "next-auth.session-token.0", "__Secure-next-auth.session-token.12",
		"authjs.session-token", "__Secure-authjs.session-token.1",
	} {
		normalized, err := egress.NormalizeOllamaCloudUsageCookie(allowed + "=value")
		require.NoError(t, err, allowed)
		require.Equal(t, allowed+"=value", normalized)
	}

	for _, invalid := range []string{
		"", "Domain=ollama.com; wos-session=x", "wos-session=x; Path=/",
		"wos-session=x; wos-session=y", "Secure", "tracking=only", "__session=arbitrary",
		"authjs.session-token.bad=not-a-shard", "Authjs.session-token=wrong-case",
	} {
		_, err := egress.NormalizeOllamaCloudUsageCookie(invalid)
		require.Error(t, err, invalid)
	}
	_, err = egress.NormalizeOllamaCloudUsageCookie("wos-session=" + strings.Repeat("x", ollamaCloudUsageMaxSessionBytes))
	require.ErrorContains(t, err, "too large")
}

func TestOllamaCloudUsageSessionEncryptionFailClosedAndWriteOnlyState(t *testing.T) {
	account := ollamaUsageAccount(7)
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{7: account}}}
	settings := &ollamaUsageSettings{}

	ephemeral := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{}, settings, false)
	_, err := ephemeral.SaveSession(context.Background(), 7, "wos-session=plaintext-secret")
	require.ErrorIs(t, err, accountcore.ErrOllamaCloudUsageEncryptionKey)
	require.NotContains(t, account.Extra, accountcore.OllamaCloudUsageSessionExtraKey)

	svc := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{}, settings, true)
	_, err = svc.SaveSession(context.Background(), 7, "tracking=arbitrary-only")
	require.Error(t, err)
	require.NotContains(t, account.Extra, accountcore.OllamaCloudUsageSessionExtraKey)

	state, err := svc.SaveSession(context.Background(), 7, "tracking=must-not-persist; wos-session=plaintext-secret")
	require.NoError(t, err)
	require.True(t, state.Configured)
	stored, ok := account.Extra[accountcore.OllamaCloudUsageSessionExtraKey].(string)
	require.True(t, ok)
	require.Equal(t, "cipher:wos-session=plaintext-secret", stored)
	require.NotContains(t, stored, "tracking")
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "plaintext-secret")
	require.NotContains(t, string(raw), "cipher:")

	account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "plaintext-secret"
	_, err = svc.Refresh(context.Background(), 7)
	require.ErrorContains(t, err, "cannot be decrypted")
}

func TestOllamaCloudUsageGroupSharesAcrossPlatformsURLVariantsAndDynamicSiblings(t *testing.T) {
	source := ollamaUsageAccount(71)
	source.Credentials["api_key"] = "shared-key"
	source.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=shared"
	source.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = true
	source.Extra[accountcore.OllamaCloudUsageSnapshotExtraKey] = &accountcore.OllamaCloudUsageSnapshot{
		Status: accountcore.OllamaCloudUsageStatusOK,
		Data:   &accountcore.OllamaCloudUsageData{Plan: "pro"},
	}
	source.UpdatedAt = time.Now().Add(-time.Minute)
	sibling := ollamaUsageAccount(72)
	sibling.Platform = capability.PlatformAnthropic
	sibling.Credentials = map[string]any{"base_url": "HTTPS://WWW.OLLAMA.COM:443/v1", "api_key": "shared-key"}
	sibling.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=shared"
	sibling.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = true
	sibling.UpdatedAt = time.Now()
	different := ollamaUsageAccount(73)
	different.Credentials["api_key"] = "different-key"
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{
		source.ID: source, sibling.ID: sibling, different.ID: different,
	}}}
	svc := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{}, &ollamaUsageSettings{}, true)

	state, err := svc.GetState(context.Background(), sibling.ID)
	require.NoError(t, err)
	require.True(t, state.Configured)
	require.True(t, state.AutoRefreshEnabled)
	require.Equal(t, "pro", state.Snapshot.Data.Plan)

	differentState, err := svc.GetState(context.Background(), different.ID)
	require.NoError(t, err)
	require.False(t, differentState.Configured)

	newSibling := ollamaUsageAccount(74)
	newSibling.Platform = capability.PlatformAnthropic
	newSibling.Credentials = map[string]any{"base_url": "https://ollama.com:443", "api_key": "shared-key"}
	repo.mu.Lock()
	repo.accounts[newSibling.ID] = newSibling
	repo.mu.Unlock()
	newState, err := svc.GetState(context.Background(), newSibling.ID)
	require.NoError(t, err)
	require.True(t, newState.Configured)
	require.Equal(t, state.Snapshot, newState.Snapshot)

	before := repo.groupResolveCalls.Load()
	require.NoError(t, svc.ResolveAccounts(context.Background(), []*accountcore.Record{source, sibling, different, newSibling}))
	require.Equal(t, before+1, repo.groupResolveCalls.Load(), "one list batch must issue one group lookup")
}

func TestOllamaCloudUsageSaveAutoRefreshAndDeleteAreGroupScoped(t *testing.T) {
	first := ollamaUsageAccount(81)
	first.Credentials["api_key"] = "shared-key"
	second := ollamaUsageAccount(82)
	second.Platform = capability.PlatformAnthropic
	second.Credentials = map[string]any{"base_url": "https://www.ollama.com/v1", "api_key": "shared-key"}
	different := ollamaUsageAccount(83)
	different.Credentials["api_key"] = "different-key"
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{
		first.ID: first, second.ID: second, different.ID: different,
	}}}
	svc := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{}, &ollamaUsageSettings{}, true)

	state, err := svc.SaveSession(context.Background(), second.ID, "wos-session=shared-browser")
	require.NoError(t, err)
	require.True(t, state.Configured)
	require.Equal(t, "cipher:wos-session=shared-browser", first.Extra[accountcore.OllamaCloudUsageSessionExtraKey])
	require.Equal(t, first.Extra[accountcore.OllamaCloudUsageSessionExtraKey], second.Extra[accountcore.OllamaCloudUsageSessionExtraKey])
	require.NotContains(t, different.Extra, accountcore.OllamaCloudUsageSessionExtraKey)

	state, err = svc.SetAutoRefresh(context.Background(), first.ID, true)
	require.NoError(t, err)
	require.True(t, state.AutoRefreshEnabled)
	require.Equal(t, true, first.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey])
	require.Equal(t, true, second.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey])

	state, err = svc.DeleteSession(context.Background(), second.ID)
	require.NoError(t, err)
	require.False(t, state.Configured)
	for _, member := range []*accountcore.Record{first, second} {
		require.NotContains(t, member.Extra, accountcore.OllamaCloudUsageSessionExtraKey)
		require.NotContains(t, member.Extra, accountcore.OllamaCloudUsageAutoRefreshExtraKey)
		require.NotContains(t, member.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
	}
}

func TestOllamaCloudUsageRefreshSingleflightAndRunnerDeduplicateSharedGroup(t *testing.T) {
	first := ollamaUsageAccount(91)
	first.Credentials["api_key"] = "shared-key"
	first.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=shared"
	first.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = true
	second := ollamaUsageAccount(92)
	second.Platform = capability.PlatformAnthropic
	second.Credentials = map[string]any{"base_url": "https://www.ollama.com:443/v1", "api_key": "shared-key"}
	second.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=shared"
	second.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = true
	repo := &ollamaUsageTestRepo{
		ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{first.ID: first, second.ID: second}},
		due:             []accountcore.Record{*first, *second},
	}
	settingsRepo := &ollamaUsageSettings{values: map[string]string{
		accountcore.SettingKeyOllamaCloudUsageSettings: `{"enabled":true,"interval_minutes":60}`,
	}}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t), beforeResponse: func(*http.Request) {
		once.Do(func() { close(started) })
		<-release
	}}
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo, true)

	errs := make(chan error, 2)
	go func() { _, err := svc.Refresh(context.Background(), first.ID); errs <- err }()
	<-started
	// 首个调用方已完成构造分组键和 singleflight 内部的两次账号读取，此时阻塞在上游 stub。
	loadsBeforeSecond := repo.getByIDCalls.Load()
	go func() { _, err := svc.Refresh(context.Background(), second.ID); errs <- err }()
	// 第二个调用方完成自己的账号读取后才释放首个请求，确保它加入同一个在途 singleflight。
	// 否则它可能在首个请求完成后另起执行，并被刚写入的 30 秒手动刷新限流拒绝。
	require.Eventually(t, func() bool {
		return repo.getByIDCalls.Load() > loadsBeforeSecond
	}, 5*time.Second, time.Millisecond, "第二个调用方必须在释放首个请求前到达 singleflight")
	close(release)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.Equal(t, int64(1), upstream.calls.Load())
	require.NotNil(t, accountcore.DecodeOllamaCloudUsageSnapshot(first.Extra))
	require.Equal(t, accountcore.DecodeOllamaCloudUsageSnapshot(first.Extra), accountcore.DecodeOllamaCloudUsageSnapshot(second.Extra))

	delete(first.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
	delete(second.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
	upstream.beforeResponse = nil
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(2), upstream.calls.Load(), "RunDue must issue one request for the shared group")
}

func TestOllamaCloudUsageRefreshRejectsGroupChangeBeforeUpstreamRequest(t *testing.T) {
	account := ollamaUsageAccount(94)
	account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=secret"
	base := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{account.ID: account}}}
	repo := &ollamaRefreshPreflightIdentityChangeRepo{ollamaUsageTestRepo: base}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageContract(repo, upstream, &ollamaUsageSettings{}, ollamaUsageTestEncryptor{}, true)
	t.Cleanup(svc.Stop)

	_, err := svc.Refresh(context.Background(), account.ID)

	require.ErrorIs(t, err, accountcore.ErrOllamaCloudUsageIdentityChanged)
	require.Zero(t, upstream.calls.Load())
	require.NotContains(t, account.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
}

func TestOllamaCloudUsageRefreshUsesFixedURLCookieAndNoRedirects(t *testing.T) {
	account := ollamaUsageAccount(8)
	account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=browser-secret; tracking=must-not-send"
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{8: account}}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, &ollamaUsageSettings{}, true)
	fixedNow := time.Date(2026, time.July, 22, 15, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	state, err := svc.Refresh(context.Background(), 8)
	require.NoError(t, err)
	require.Equal(t, accountcore.OllamaCloudUsageStatusOK, state.Snapshot.Status)
	require.Equal(t, "https://ollama.com/settings", upstream.lastRequest.URL.String())
	require.Equal(t, "ollama.com", upstream.lastRequest.Host)
	require.Equal(t, "wos-session=browser-secret", upstream.lastRequest.Header.Get("Cookie"))
	require.NotContains(t, upstream.lastRequest.Header.Get("Cookie"), "tracking")
	require.Empty(t, upstream.lastRequest.Header.Get("Authorization"))
	require.True(t, upstreamcore.HTTPUpstreamRedirectsDisabled(upstream.lastRequest.Context()))
}

func TestOllamaCloudUsageManualRefreshUsesShortIndependentInterval(t *testing.T) {
	account := ollamaUsageAccount(12)
	account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=initial"
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{12: account}}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, &ollamaUsageSettings{}, true)
	fixedNow := time.Date(2026, time.July, 22, 15, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	_, err := svc.Refresh(context.Background(), 12)
	require.NoError(t, err)
	_, err = svc.Refresh(context.Background(), 12)
	require.ErrorIs(t, err, accountcore.ErrOllamaCloudUsageRefreshRateLimited)
	require.Equal(t, int64(1), upstream.calls.Load())

	// 保存修复后的会话会清除旧快照，避免全局 60 分钟 next_refresh_at 阻止管理员立即验证。
	_, err = svc.SaveSession(context.Background(), 12, "wos-session=repaired")
	require.NoError(t, err)
	_, err = svc.Refresh(context.Background(), 12)
	require.NoError(t, err)
	require.Equal(t, int64(2), upstream.calls.Load())
}

func TestOllamaCloudUsageRefreshUsesHydratedProxyIdentity(t *testing.T) {
	account := ollamaUsageAccount(13)
	account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=secret"
	proxyID := int64(4)
	account.ProxyID = &proxyID
	account.Proxy = &egress.Proxy{
		ID: proxyID, Protocol: "http", Host: "127.0.0.1", Port: 3128,
		Username: "proxy-user", Password: "proxy-pass", Status: accountcore.StatusActive,
	}
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{13: account}}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, &ollamaUsageSettings{}, true)

	_, err := svc.Refresh(context.Background(), 13)
	require.NoError(t, err)
	require.Equal(t, account.Proxy.URL(), upstream.lastProxyURL)
}

func TestOllamaCloudUsageRedirectAndBodyLimitArePersistedSafely(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   []byte
		reason string
	}{
		{"redirect", http.StatusFound, nil, "redirect_blocked"},
		{"body limit", http.StatusOK, make([]byte, upstreamollama.MaxBodyBytes+1), "response_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := ollamaUsageAccount(9)
			account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=secret"
			repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{9: account}}}
			svc := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{status: test.status, body: test.body}, &ollamaUsageSettings{}, true)
			state, err := svc.Refresh(context.Background(), 9)
			require.NoError(t, err)
			require.Equal(t, accountcore.OllamaCloudUsageStatusFailed, state.Snapshot.Status)
			require.Equal(t, test.reason, state.Snapshot.LastError)
		})
	}
}

func TestOllamaCloudUsageRefreshRejectsIdentityChange(t *testing.T) {
	account := ollamaUsageAccount(10)
	account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=secret"
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{10: account}}}
	repo.beforeSnapshot = func() { account.Credentials["api_key"] = "rotated" }
	svc := newOllamaUsageTestService(t, repo, &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}, &ollamaUsageSettings{}, true)
	_, err := svc.Refresh(context.Background(), 10)
	require.ErrorIs(t, err, accountcore.ErrOllamaCloudUsageIdentityChanged)
	require.NotContains(t, account.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
}

func TestOllamaCloudUsageRunnerHonorsLeaderLockAndBackoff(t *testing.T) {
	account := ollamaUsageAccount(11)
	account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=secret"
	account.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = true
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{11: account}}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	settingsRepo := &ollamaUsageSettings{values: map[string]string{
		accountcore.SettingKeyOllamaCloudUsageSettings: `{"enabled":true,"interval_minutes":60}`,
	}}
	cache := &ollamaUsageLeader{}
	_, acquired := accountcore.AcquireSingletonLease(context.Background(), cache, nil, ollamaCloudUsageLeaderLockKey, "peer", time.Minute)
	require.True(t, acquired)
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo, true)
	svc.lockCache = cache
	require.NoError(t, svc.RunDue(context.Background()))
	require.Zero(t, upstream.calls.Load())
	require.NoError(t, cache.ReleaseLeaderLock(context.Background(), ollamaCloudUsageLeaderLockKey, "peer"))
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), upstream.calls.Load())

	firstFailure := accountcore.NextOllamaCloudUsageDelay(60, 1, 0, rand.Int64N)
	thirdFailure := accountcore.NextOllamaCloudUsageDelay(60, 3, 0, rand.Int64N)
	require.Greater(t, thirdFailure, firstFailure)
	require.GreaterOrEqual(t, accountcore.NextOllamaCloudUsageDelay(60, 1, 3*time.Hour, rand.Int64N), 3*time.Hour)
	require.LessOrEqual(t, accountcore.NextOllamaCloudUsageDelay(60, 20, 0, rand.Int64N), accountcore.OllamaCloudUsageMaxDelay+5*time.Minute)
}

func TestOllamaCloudUsageRunnerDisablesAutoRefreshAfterUnpersistableIdentityError(t *testing.T) {
	account := ollamaUsageAccount(14)
	account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=secret"
	account.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = true
	missingProxyID := int64(99)
	account.ProxyID = &missingProxyID
	account.Proxy = nil
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{14: account}}}
	settingsRepo := &ollamaUsageSettings{values: map[string]string{
		accountcore.SettingKeyOllamaCloudUsageSettings: `{"enabled":true,"interval_minutes":60}`,
	}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo, true)

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), repo.disableAutoCalls.Load())
	require.Equal(t, false, account.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey])
	require.Zero(t, upstream.calls.Load())

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), repo.disableAutoCalls.Load())
	require.Zero(t, upstream.calls.Load())
}

func TestOllamaCloudUsageRunnerIdentityChangePreservesOldGroupAndDoesNotLoop(t *testing.T) {
	anchor := ollamaUsageAccount(15)
	anchor.Credentials["api_key"] = "shared-before-rotation"
	anchor.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=secret"
	anchor.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = true
	sibling := ollamaUsageAccount(16)
	sibling.Platform = capability.PlatformAnthropic
	sibling.Credentials = map[string]any{"api_key": "shared-before-rotation", "base_url": "https://www.ollama.com:443/v1"}
	sibling.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=secret"
	sibling.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = true
	dueAnchor := *anchor
	dueAnchor.Credentials = accountcore.CRSMergeMap(nil, anchor.Credentials)
	dueAnchor.Extra = accountcore.CRSMergeMap(nil, anchor.Extra)
	repo := &ollamaUsageTestRepo{
		ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*accountcore.Record{
			anchor.ID: anchor, sibling.ID: sibling,
		}},
		due: []accountcore.Record{dueAnchor},
	}
	var rotateOnce sync.Once
	repo.beforeSnapshot = func() {
		rotateOnce.Do(func() {
			repo.mu.Lock()
			defer repo.mu.Unlock()
			anchor.Credentials["api_key"] = "rotated-account-key"
			delete(anchor.Extra, accountcore.OllamaCloudUsageSessionExtraKey)
			delete(anchor.Extra, accountcore.OllamaCloudUsageAutoRefreshExtraKey)
			delete(anchor.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
		})
	}
	settingsRepo := &ollamaUsageSettings{values: map[string]string{
		accountcore.SettingKeyOllamaCloudUsageSettings: `{"enabled":true,"interval_minutes":60}`,
	}}
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t)}
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo, true)

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), repo.disableAutoAttempts.Load())
	require.Zero(t, repo.disableAutoCalls.Load(), "the stale anchor CAS must not disable the old sibling group")
	require.Equal(t, true, sibling.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey])
	require.NotContains(t, anchor.Extra, accountcore.OllamaCloudUsageAutoRefreshExtraKey)

	repo.due = []accountcore.Record{*anchor, *sibling}
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), repo.disableAutoAttempts.Load(), "the changed account must not be retried")
	require.Equal(t, true, sibling.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey])
	require.NotNil(t, accountcore.DecodeOllamaCloudUsageSnapshot(sibling.Extra), "the still-valid sibling must refresh normally")
	require.Equal(t, int64(2), upstream.calls.Load())
}

func TestOllamaCloudUsageSingleflightConcurrencyAndRunnerSwitches(t *testing.T) {
	accounts := make(map[int64]*accountcore.Record)
	for id := int64(1); id <= 7; id++ {
		account := ollamaUsageAccount(id)
		account.Extra[accountcore.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=secret"
		account.Extra[accountcore.OllamaCloudUsageAutoRefreshExtraKey] = true
		accounts[id] = account
	}
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: accounts}}
	unblock := make(chan struct{})
	entered := make(chan struct{}, 10)
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t), beforeResponse: func(*http.Request) {
		entered <- struct{}{}
		<-unblock
	}}
	settingsRepo := &ollamaUsageSettings{values: map[string]string{}}
	svc := newOllamaUsageTestService(t, repo, upstream, settingsRepo, true)

	// 全局自动刷新默认安全关闭。
	require.NoError(t, svc.RunDue(context.Background()))
	require.Zero(t, upstream.calls.Load())

	settingsRepo.values[accountcore.SettingKeyOllamaCloudUsageSettings] = `{"enabled":true,"interval_minutes":60}`
	var singleflight sync.WaitGroup
	singleflight.Add(2)
	for range 2 {
		go func() {
			defer singleflight.Done()
			_, _ = svc.Refresh(context.Background(), 1)
		}()
	}
	<-entered
	close(unblock)
	singleflight.Wait()
	require.Equal(t, int64(1), upstream.calls.Load())

	// 清除快照使所有账号到期，再验证共享的四槽并发上限。
	for _, account := range accounts {
		delete(account.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)
	}
	unblock2 := make(chan struct{})
	upstream.beforeResponse = func(*http.Request) { <-unblock2 }
	done := make(chan struct{})
	go func() {
		_ = svc.RunDue(context.Background())
		close(done)
	}()
	require.Eventually(t, func() bool { return upstream.active.Load() == ollamaCloudUsageConcurrency }, time.Second, 10*time.Millisecond)
	close(unblock2)
	<-done
	require.LessOrEqual(t, upstream.maxActive.Load(), int64(ollamaCloudUsageConcurrency))
	require.Equal(t, int64(8), upstream.calls.Load())
}
