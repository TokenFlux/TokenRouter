// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	"context"
	"strings"
	"time"
)

// DingTalkSyncRuntime 持有配置读取与任务端口，保存原后台同步时机和 30 秒预算。
type DingTalkSyncRuntime struct {
	LoadConfig func(context.Context) (DingTalkOAuthOptions, error)
	Client     func(DingTalkOAuthOptions) DingTalkOAuthClient
	Profiles   *DingTalkProfileSync
	Run        func(string, func()) bool
	Observe    ProfileSyncObserver
}

func (s *DingTalkSyncRuntime) Async(ctx context.Context, cfg DingTalkOAuthOptions, client DingTalkOAuthClient, id int64, staff *DingTalkProfileSnapshot, username bool) {
	RunDingTalkSync(ctx, s.Run, s.Observe, func(ctx context.Context) { s.Sync(ctx, cfg, client, id, staff, username) })
}
func (s *DingTalkSyncRuntime) Sync(ctx context.Context, cfg DingTalkOAuthOptions, client DingTalkOAuthClient, id int64, staff *DingTalkProfileSnapshot, username bool) {
	s.Profiles.Sync(ctx, DingTalkSyncOptions{CorpRestrictionPolicy: cfg.CorpRestrictionPolicy, SyncCorpEmail: cfg.SyncCorpEmail, SyncDisplayName: cfg.SyncDisplayName, SyncDept: cfg.SyncDept, SyncCorpEmailAttrKey: cfg.SyncCorpEmailAttrKey, SyncDisplayNameAttrKey: cfg.SyncDisplayNameAttrKey, SyncDeptAttrKey: cfg.SyncDeptAttrKey}, client, id, staff, username)
}
func (s *DingTalkSyncRuntime) FromClaims(ctx context.Context, cfg DingTalkOAuthOptions, client DingTalkOAuthClient, id int64, claims map[string]any, username bool) {
	s.Async(ctx, cfg, client, id, DingTalkProfileFromClaims(claims), username)
}
func (s *DingTalkSyncRuntime) Pending(ctx context.Context, p *PendingAuthSession, id int64, username bool) {
	if p == nil || id <= 0 || !strings.EqualFold(strings.TrimSpace(p.ProviderType), "dingtalk") {
		return
	}
	cfg, e := s.LoadConfig(ctx)
	if e != nil {
		if s.Observe != nil {
			s.Observe("debug", "dingtalk sync: skip post-login sync, config unavailable", "user_id", id, "err", e.Error())
		}
		return
	}
	s.FromClaims(ctx, cfg, s.Client(cfg), id, p.UpstreamIdentityClaims, username)
}

// RunDingTalkSync 与请求取消解耦但保留值，任务是否接受及退出等待由组合根负责。
func RunDingTalkSync(parent context.Context, run func(string, func()) bool, observe ProfileSyncObserver, fn func(context.Context)) {
	if run == nil {
		return
	}
	base := context.WithoutCancel(parent)
	run("handler/auth_dingtalk_oauth.go:runDingTalkSyncAsync", func() {
		defer func() {
			if r := recover(); r != nil && observe != nil {
				observe("error", "dingtalk sync: panic recovered", "panic", r)
			}
		}()
		ctx, cancel := context.WithTimeout(base, 30*time.Second)
		defer cancel()
		fn(ctx)
	})
}
