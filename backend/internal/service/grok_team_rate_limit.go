package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// 以下为 Grok OAuth 的进程内 team+model 限流覆盖：同一 team_id 的某账号在模型上被限流后，
// 其它账号在冷却期内跳过该模型。多实例各自从本实例 429 学习，短 TTL 让状态漂移自行收敛。
type grokTeamModelRateLimit struct {
	Until time.Time
}

type grokTeamModelRateLimitStore struct {
	mu    sync.Mutex
	items map[string]grokTeamModelRateLimit
}

var globalGrokTeamModelRateLimits = &grokTeamModelRateLimitStore{
	items: make(map[string]grokTeamModelRateLimit),
}

const (
	grokTeamRateLimitDefaultTTL = 10 * time.Minute
	grokTeamRateLimitMaxTTL     = time.Hour
	grokTeamRateLimitMinTTL     = 30 * time.Second
)

func grokTeamFingerprint(teamID string) string {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.ToLower(teamID)))
	return hex.EncodeToString(sum[:8])
}

func grokTeamModelRateLimitKey(teamFingerprint, model string) string {
	return teamFingerprint + "|" + strings.ToLower(strings.TrimSpace(model))
}

func accountGrokTeamID(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential("team_id"))
}

// markGrokTeamModelRateLimit 记录 team+model 在截止时间前应被跳过；team_id 或模型为空时无操作。
func markGrokTeamModelRateLimit(account *Account, model string, until time.Time) {
	if account == nil || !account.IsGrokOAuth() {
		return
	}
	fp := grokTeamFingerprint(accountGrokTeamID(account))
	model = strings.TrimSpace(model)
	if fp == "" || model == "" || until.IsZero() {
		return
	}
	now := time.Now()
	if !until.After(now) {
		until = now.Add(grokTeamRateLimitDefaultTTL)
	}
	maxUntil := now.Add(grokTeamRateLimitMaxTTL)
	if until.After(maxUntil) {
		until = maxUntil
	}
	key := grokTeamModelRateLimitKey(fp, model)
	globalGrokTeamModelRateLimits.mu.Lock()
	defer globalGrokTeamModelRateLimits.mu.Unlock()
	if cur, ok := globalGrokTeamModelRateLimits.items[key]; ok && cur.Until.After(until) {
		return
	}
	globalGrokTeamModelRateLimits.items[key] = grokTeamModelRateLimit{Until: until}
	// 顺带清理已过期条目。
	for k, v := range globalGrokTeamModelRateLimits.items {
		if !v.Until.After(now) {
			delete(globalGrokTeamModelRateLimits.items, k)
		}
	}
}

// isGrokTeamModelRateLimited 判断账号所属团队当前是否被指定模型限流。
func isGrokTeamModelRateLimited(account *Account, model string, now time.Time) bool {
	if account == nil || !account.IsGrokOAuth() {
		return false
	}
	fp := grokTeamFingerprint(accountGrokTeamID(account))
	model = strings.TrimSpace(model)
	if fp == "" || model == "" {
		return false
	}
	key := grokTeamModelRateLimitKey(fp, model)
	globalGrokTeamModelRateLimits.mu.Lock()
	defer globalGrokTeamModelRateLimits.mu.Unlock()
	cur, ok := globalGrokTeamModelRateLimits.items[key]
	if !ok {
		return false
	}
	if !cur.Until.After(now) {
		delete(globalGrokTeamModelRateLimits.items, key)
		return false
	}
	return true
}

// filterGrokTeamModelRateLimitedAccounts 移除团队处于模型级冷却的候选；没有 team_id 的账号直接通过。
func filterGrokTeamModelRateLimitedAccounts(accounts []Account, model string, now time.Time) []Account {
	if len(accounts) == 0 || strings.TrimSpace(model) == "" {
		return accounts
	}
	out := accounts[:0]
	kept := false
	for i := range accounts {
		upstreamModel := canonicalOpenAIAccountSchedulingModel(&accounts[i], model)
		if isGrokTeamModelRateLimited(&accounts[i], upstreamModel, now) {
			continue
		}
		out = append(out, accounts[i])
		kept = true
	}
	if !kept && len(out) == 0 {
		// 全部被过滤时返回空集合，由调用方按无容量处理。
		return nil
	}
	return out
}

// resolveGrokTeamRateLimitUntil 从账号限流重置时间推导团队冷却窗口并限制合理范围。
func resolveGrokTeamRateLimitUntil(resetAt, now time.Time) time.Time {
	if resetAt.After(now.Add(grokTeamRateLimitMinTTL)) {
		maxUntil := now.Add(grokTeamRateLimitMaxTTL)
		if resetAt.After(maxUntil) {
			return maxUntil
		}
		return resetAt
	}
	return now.Add(grokTeamRateLimitDefaultTTL)
}
