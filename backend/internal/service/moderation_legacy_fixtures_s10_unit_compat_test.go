//go:build unit

// 保留原标签测试的私有委托，生产代码不保留测试专用入口。
package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type contentModerationTestSettingRepo struct {
	values map[string]string
}

func (r *contentModerationTestSettingRepo) Get(ctx context.Context, key string) (*Setting, error) {
	if value, ok := r.values[key]; ok {
		return &Setting{Key: key, Value: value}, nil
	}
	return nil, ErrSettingNotFound
}

func (r *contentModerationTestSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (r *contentModerationTestSettingRepo) Set(ctx context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

func (r *contentModerationTestSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *contentModerationTestSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *contentModerationTestSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *contentModerationTestSettingRepo) Delete(ctx context.Context, key string) error {
	delete(r.values, key)
	return nil
}

type contentModerationTestRepo struct {
	mu            sync.Mutex
	logs          []ContentModerationLog
	cyberWarnings []ContentModerationCyberWarning
}

func (r *contentModerationTestRepo) CreateLog(ctx context.Context, log *ContentModerationLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if log != nil {
		r.logs = append(r.logs, *log)
	}
	return nil
}

func (r *contentModerationTestRepo) ListLogs(ctx context.Context, filter ContentModerationLogFilter) ([]ContentModerationLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *contentModerationTestRepo) CountFlaggedByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, log := range r.logs {
		if log.UserID == nil || *log.UserID != userID || !log.Flagged || log.Action == ContentModerationActionHashBlock {
			continue
		}
		if log.CreatedAt.IsZero() || log.CreatedAt.Before(since) {
			continue
		}
		count++
	}
	return count, nil
}

func (r *contentModerationTestRepo) CreateCyberWarning(ctx context.Context, warning *ContentModerationCyberWarning) error {
	if warning != nil {
		if warning.CreatedAt.IsZero() {
			warning.CreatedAt = time.Now()
		}
		r.cyberWarnings = append(r.cyberWarnings, *warning)
	}
	return nil
}

func (r *contentModerationTestRepo) CreateCyberWarningAndApplyUserBan(ctx context.Context, warning *ContentModerationCyberWarning, policy ContentModerationCyberWarningPolicy) (bool, error) {
	if warning == nil {
		return false, nil
	}
	count := 1
	if warning.UserID != nil && policy.WindowHours > 0 {
		since := time.Now().Add(-time.Duration(policy.WindowHours) * time.Hour)
		n, _ := r.CountCyberWarningsByUserSince(ctx, *warning.UserID, since)
		count = n + 1
	}
	warning.ViolationCount = count
	if policy.AutoBanEnabled && policy.BanThreshold > 0 && count >= policy.BanThreshold {
		warning.AutoBanned = true
	}
	if warning.CreatedAt.IsZero() {
		warning.CreatedAt = time.Now()
	}
	r.cyberWarnings = append(r.cyberWarnings, *warning)
	return warning.AutoBanned, nil
}

func (r *contentModerationTestRepo) ListCyberWarnings(ctx context.Context, filter ContentModerationCyberWarningFilter) ([]ContentModerationCyberWarning, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *contentModerationTestRepo) CountCyberWarningsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	count := 0
	for _, warning := range r.cyberWarnings {
		if warning.UserID != nil && *warning.UserID == userID && warning.CreatedAt.After(since) {
			count++
		}
	}
	return count, nil
}

func (r *contentModerationTestRepo) GetCyberSummary(ctx context.Context, filter ContentModerationCyberWarningFilter) (*ContentModerationCyberSummary, error) {
	return &ContentModerationCyberSummary{}, nil
}

func (r *contentModerationTestRepo) MarkCyberWarningEmailSent(ctx context.Context, id int64) error {
	return nil
}

func (r *contentModerationTestRepo) CleanupExpiredLogs(ctx context.Context, hitBefore time.Time, nonHitBefore time.Time) (*ContentModerationCleanupResult, error) {
	return &ContentModerationCleanupResult{}, nil
}

func (r *contentModerationTestRepo) snapshotLogs() []ContentModerationLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ContentModerationLog, len(r.logs))
	copy(out, r.logs)
	return out
}

func requireContentModerationLogCount(t *testing.T, repo *contentModerationTestRepo, want int) []ContentModerationLog {
	t.Helper()
	var logs []ContentModerationLog
	require.Eventually(t, func() bool {
		logs = repo.snapshotLogs()
		return len(logs) == want
	}, time.Second, 10*time.Millisecond)
	return logs
}

type contentModerationTestHashCache struct {
	mu            sync.Mutex
	hashes        map[string]struct{}
	recorded      []string
	checked       []string
	deleted       []string
	hasResult     bool
	hasResultUsed bool
}

func (c *contentModerationTestHashCache) RecordFlaggedInputHash(ctx context.Context, inputHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hashes == nil {
		c.hashes = map[string]struct{}{}
	}
	c.hashes[inputHash] = struct{}{}
	c.recorded = append(c.recorded, inputHash)
	return nil
}

func (c *contentModerationTestHashCache) HasFlaggedInputHash(ctx context.Context, inputHash string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = append(c.checked, inputHash)
	if c.hasResultUsed {
		return c.hasResult, nil
	}
	_, ok := c.hashes[inputHash]
	return ok, nil
}

func (c *contentModerationTestHashCache) DeleteFlaggedInputHash(ctx context.Context, inputHash string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, inputHash)
	if c.hashes == nil {
		return false, nil
	}
	if _, ok := c.hashes[inputHash]; !ok {
		return false, nil
	}
	delete(c.hashes, inputHash)
	return true, nil
}

func (c *contentModerationTestHashCache) ClearFlaggedInputHashes(ctx context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	deleted := int64(len(c.hashes))
	c.hashes = map[string]struct{}{}
	return deleted, nil
}

func (c *contentModerationTestHashCache) CountFlaggedInputHashes(ctx context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(len(c.hashes)), nil
}

func (c *contentModerationTestHashCache) snapshotRecorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.recorded))
	copy(out, c.recorded)
	return out
}
