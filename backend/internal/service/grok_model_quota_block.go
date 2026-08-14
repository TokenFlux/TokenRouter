package service

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// 此结构保存进程内按账号和模型划分的 Grok 免费额度软性阻断记录。
// 当错误明确指出某个模型额度耗尽时，同一账号的其他模型仍可调度；
// 多实例部署中，每个进程分别从自身收到的上游错误学习该状态。
type grokModelQuotaBlock struct {
	Until time.Time
}

type grokModelQuotaBlockStore struct {
	mu    sync.Mutex
	items map[string]grokModelQuotaBlock // 键格式为 accountID|model。
}

var globalGrokModelQuotaBlocks = &grokModelQuotaBlockStore{
	items: make(map[string]grokModelQuotaBlock),
}

const (
	grokModelQuotaBlockDefaultTTL = 2 * time.Hour
	grokModelQuotaBlockMaxTTL     = 6 * time.Hour
	grokModelQuotaBlockMinTTL     = 20 * time.Minute
)

func grokModelQuotaBlockKey(accountID int64, model string) string {
	return strings.TrimSpace(strings.ToLower(model)) + "|" + strconv.FormatInt(accountID, 10)
}

// markGrokModelQuotaBlock 将账号指定模型软性阻断到给定时间。
func markGrokModelQuotaBlock(accountID int64, model string, until time.Time) {
	model = strings.TrimSpace(model)
	if accountID <= 0 || model == "" || until.IsZero() {
		return
	}
	now := time.Now()
	if !until.After(now.Add(grokModelQuotaBlockMinTTL)) {
		until = now.Add(grokModelQuotaBlockDefaultTTL)
	}
	if max := now.Add(grokModelQuotaBlockMaxTTL); until.After(max) {
		until = max
	}
	storeGrokModelQuotaBlock(accountID, model, until, now)
}

const (
	grokModelTransientBlockMinTTL = 500 * time.Millisecond
	grokModelTransientBlockMaxTTL = 5 * time.Minute
)

// markGrokModelTransientBlock 在短时容量波动时仅软阻断单个模型，
// 不使用免费额度的 20 分钟下限，也不暂停整个账号。
func markGrokModelTransientBlock(accountID int64, model string, until time.Time) {
	model = strings.TrimSpace(model)
	if accountID <= 0 || model == "" || until.IsZero() {
		return
	}
	now := time.Now()
	if !until.After(now.Add(grokModelTransientBlockMinTTL)) {
		until = now.Add(grokModelTransientBlockMinTTL)
	}
	if max := now.Add(grokModelTransientBlockMaxTTL); until.After(max) {
		until = max
	}
	storeGrokModelQuotaBlock(accountID, model, until, now)
}

func storeGrokModelQuotaBlock(accountID int64, model string, until, now time.Time) {
	key := grokModelQuotaBlockKey(accountID, model)
	globalGrokModelQuotaBlocks.mu.Lock()
	defer globalGrokModelQuotaBlocks.mu.Unlock()
	if cur, ok := globalGrokModelQuotaBlocks.items[key]; ok && cur.Until.After(until) {
		return
	}
	globalGrokModelQuotaBlocks.items[key] = grokModelQuotaBlock{Until: until}
	for k, v := range globalGrokModelQuotaBlocks.items {
		if !v.Until.After(now) {
			delete(globalGrokModelQuotaBlocks.items, k)
		}
	}
}

// isGrokModelQuotaBlocked 判断账号当前是否无法服务指定模型。
func isGrokModelQuotaBlocked(accountID int64, model string, now time.Time) bool {
	model = strings.TrimSpace(model)
	if accountID <= 0 || model == "" {
		return false
	}
	key := grokModelQuotaBlockKey(accountID, model)
	globalGrokModelQuotaBlocks.mu.Lock()
	defer globalGrokModelQuotaBlocks.mu.Unlock()
	cur, ok := globalGrokModelQuotaBlocks.items[key]
	if !ok {
		return false
	}
	if !cur.Until.After(now) {
		delete(globalGrokModelQuotaBlocks.items, key)
		return false
	}
	return true
}

func filterGrokModelQuotaBlockedAccounts(accounts []Account, model string, now time.Time) []Account {
	if len(accounts) == 0 || strings.TrimSpace(model) == "" {
		return accounts
	}
	out := make([]Account, 0, len(accounts))
	for i := range accounts {
		upstreamModel := canonicalOpenAIAccountSchedulingModel(&accounts[i], model)
		if isGrokModelQuotaBlocked(accounts[i].ID, upstreamModel, now) {
			continue
		}
		out = append(out, accounts[i])
	}
	return out
}

// isGrokModelSpecificFreeUsage 判断免费额度耗尽是否仅针对指定模型，
// 此时账号仍可能服务其他模型。
func isGrokModelSpecificFreeUsage(low, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || low == "" {
		return false
	}
	if strings.Contains(low, "for model") || strings.Contains(low, "模型") {
		return true
	}
	// 兼容形如“used all the included free usage for model grok-4.5”的上游错误。
	if strings.Contains(low, "free usage") && strings.Contains(low, model) {
		return true
	}
	return false
}
