package app

import (
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/moderation/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// newHTTPModeration 仅装配实际审核核心与本地协议客户端；后台工作由当前测试等待。
func newHTTPModeration(t *testing.T, settings moderation.SettingRepository, repo moderation.ContentModerationRepository) *moderation.ContentModerationService {
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
