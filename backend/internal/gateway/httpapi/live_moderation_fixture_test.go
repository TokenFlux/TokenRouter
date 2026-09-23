package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/moderation/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// newLiveModerationRuntime 仅装配实际审核核心与本地协议客户端；后台工作由当前测试等待。
func newLiveModerationRuntime(t *testing.T, settings moderation.SettingRepository, repo moderation.ContentModerationRepository) *moderation.ContentModerationService {
	t.Helper()
	var background sync.WaitGroup
	t.Cleanup(background.Wait)
	core := moderation.NewContentModerationService(settings, repo, nil, nil, nil, nil, nil, moderation.Runtime{
		Audit:         provider.NewAuditClient(),
		SnapshotMedia: provider.SnapshotMedia,
		Background:    func(_ string, fn func()) { background.Go(fn) },
		CyberText:     openai.IsOpenAICyberWarningText,
		CyberPolicy:   openai.DetectOpenAICyberPolicy,
		ErrorMessage:  upstream.ExtractErrorMessage,
		MissingRow:    func(err error) bool { return errors.Is(err, sql.ErrNoRows) },
		MissingUser:   func(err error) bool { return errors.Is(err, identity.ErrUserNotFound) },
	})
	t.Cleanup(func() {
		if err := core.Stop(); err != nil {
			t.Errorf("停止审核运行时: %v", err)
		}
	})
	return core
}

// liveModerationSettings 只供应原 HTTP 测试的设置快照，实际审核仍执行原生服务与 HTTP 客户端。
type liveModerationSettings struct {
	settings.Repository
	values map[string]string
}

func (s *liveModerationSettings) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", settings.ErrSettingNotFound
}
func (s *liveModerationSettings) Get(ctx context.Context, key string) (*settings.Setting, error) {
	v, e := s.GetValue(ctx, key)
	if e != nil {
		return nil, e
	}
	return &settings.Setting{Key: key, Value: v}, nil
}
func (s *liveModerationSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

// liveModerationLogs 保留原场景无历史违规的输入，不模拟审核裁决。
type liveModerationLogs struct {
	moderation.ContentModerationRepository
}

func (*liveModerationLogs) CreateLog(context.Context, *moderation.ContentModerationLog) error {
	return nil
}
func (*liveModerationLogs) CountFlaggedByUserSince(context.Context, int64, time.Time) (int, error) {
	return 0, nil
}
