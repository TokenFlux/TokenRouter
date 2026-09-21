// 测试夹具适配新端口；不在生产核心中保留旧实体依赖。
package moderation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/moderation/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type User = UserSnapshot
type Proxy = egress.Proxy
type UserUpdateFields struct{ Status bool }

const RoleAdmin = "admin"

var ErrUserNotFound = errors.New("user not found")

func (r *contentModerationTestUserRepo) SetStatus(ctx context.Context, id int64, status string) error {
	return r.Update(ctx, &User{ID: id, Role: r.user.Role, Status: status}, UserUpdateFields{Status: true})
}
func (r *contentModerationTestProxyRepo) Lookup(ctx context.Context, id int64, now time.Time) (ProxyInfo, error) {
	v, e := r.GetByID(ctx, id)
	if e != nil {
		return ProxyInfo{}, e
	}
	return ProxyInfo{URL: v.URL(), Name: v.Name, Status: v.Status, Address: fmt.Sprintf("%s://%s:%d", v.Protocol, v.Host, v.Port), Active: v.IsActive(), Expired: v.IsExpired(now)}, nil
}
func newTestModeration(settings SettingRepository, repo ContentModerationRepository, hash ContentModerationHashCache, groups GroupRepository, users UserCommands, auth APIKeyAuthCacheInvalidator, email RiskSender) *ContentModerationService {
	return NewContentModerationService(settings, repo, hash, groups, users, auth, email, Runtime{Audit: provider.NewAuditClient(), SnapshotMedia: provider.SnapshotMedia, Background: func(_ string, fn func()) { go fn() }, CyberText: openai.IsOpenAICyberWarningText, CyberPolicy: openai.DetectOpenAICyberPolicy, ErrorMessage: upstream.ExtractErrorMessage, MissingRow: func(e error) bool { return errors.Is(e, sql.ErrNoRows) }, MissingUser: func(e error) bool { return errors.Is(e, ErrUserNotFound) }})
}
func IsOpenAICyberWarningText(text string) bool { return openai.IsOpenAICyberWarningText(text) }

const RoleUser = "user"

func testModerationRuntime() Runtime {
	return Runtime{Audit: provider.NewAuditClient(), SnapshotMedia: provider.SnapshotMedia, Background: func(_ string, fn func()) { go fn() }, CyberText: openai.IsOpenAICyberWarningText, CyberPolicy: openai.DetectOpenAICyberPolicy, ErrorMessage: upstream.ExtractErrorMessage, MissingRow: func(e error) bool { return errors.Is(e, sql.ErrNoRows) }, MissingUser: func(e error) bool { return errors.Is(e, ErrUserNotFound) }}
}
