package account

import (
	"context"
	"errors"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
)

type PrivacyStore interface {
	UpdatePrivacyModeIfUnchanged(context.Context, UsageObservationVersion, string) (bool, error)
}
type PrivacyProxyReader interface {
	GetByID(context.Context, int64) (*egress.Proxy, error)
}

// PrivacyOptions 只提供平台交换与观察接口，核心不创建 HTTP 客户端。
type PrivacyOptions struct {
	OpenAI       func(context.Context, string, string) string
	Antigravity  func(context.Context, string, string, string) string
	AdminObserve func(string, ...any)
	Warn, Info   func(string, ...any)
}
type PrivacyService struct {
	activity operationActivity
	store    PrivacyStore
	proxies  PrivacyProxyReader
	options  PrivacyOptions
}

func NewPrivacyService(store PrivacyStore, proxies PrivacyProxyReader, options PrivacyOptions) *PrivacyService {
	noop := func(string, ...any) {}
	if options.AdminObserve == nil {
		options.AdminObserve = noop
	}
	if options.Warn == nil {
		options.Warn = noop
	}
	if options.Info == nil {
		options.Info = noop
	}
	return &PrivacyService{store: store, proxies: proxies, options: options}
}
func ShouldSkipOpenAIPrivacyEnsure(extra map[string]any) bool {
	if extra == nil {
		return false
	}
	raw, ok := extra["privacy_mode"]
	if !ok {
		return false
	}
	mode, _ := raw.(string)
	mode = strings.TrimSpace(mode)
	return mode != PrivacyModeFailed && mode != PrivacyModeCFBlocked
}
func ApplyAntigravityPrivacyMode(value *Record, mode string) {
	if value == nil || strings.TrimSpace(mode) == "" {
		return
	}
	extra := make(map[string]any, len(value.Extra)+1)
	for key, v := range value.Extra {
		extra[key] = v
	}
	extra["privacy_mode"] = mode
	value.Extra = extra
}

type privacyOperation uint8

const (
	privacyEnsure privacyOperation = iota
	privacyForce
	privacyRefresh
)

// proxyURL 区分没有代理与代理失效，后者不能退回空 URL 发起直连。
func (s *PrivacyService) proxyURL(ctx context.Context, value *Record) (string, bool) {
	if value.ProxyID == nil {
		return "", true
	}
	if s.proxies == nil {
		return "", false
	}
	proxy, err := s.proxies.GetByID(ctx, *value.ProxyID)
	if err != nil || proxy == nil {
		s.options.Warn("account_privacy_proxy_unavailable", "account_id", value.ID, "proxy_id", *value.ProxyID, "error", err)
		return "", false
	}
	url := proxy.URL()
	return url, url != ""
}
func (s *PrivacyService) apply(ctx context.Context, value *Record, platform string, operation privacyOperation) string {
	if s == nil || value == nil || value.Platform != platform || value.Type != AccountTypeOAuth {
		return ""
	}
	if platform == PlatformOpenAI {
		// 保留管理入口的影子拒绝；后台入口的资格由其刷新准入先行决定。
		if operation != privacyRefresh && value.IsCredentialShadow() {
			return ""
		}
		if s.options.OpenAI == nil {
			return ""
		}
		if operation != privacyForce && ShouldSkipOpenAIPrivacyEnsure(value.Extra) {
			return ""
		}
	} else {
		if s.options.Antigravity == nil {
			return ""
		}
		if operation != privacyForce {
			if existing, ok := value.Extra["privacy_mode"].(string); ok && existing == AntigravityPrivacySet {
				return existing
			}
		}
	}
	token, _ := value.Credentials["access_token"].(string)
	if token == "" {
		return ""
	}
	ctx, finish, err := s.activity.begin(ctx, ErrPrivacyStopped)
	if err != nil {
		return ""
	}
	defer finish()
	observed := ObserveUsageVersion(value)
	proxyURL, ok := s.proxyURL(ctx, value)
	if !ok {
		return ""
	}
	var mode string
	if platform == PlatformOpenAI {
		mode = s.options.OpenAI(ctx, token, proxyURL)
	} else {
		project, _ := value.Credentials["project_id"].(string)
		mode = s.options.Antigravity(ctx, token, project, proxyURL)
	}
	if ctx.Err() != nil {
		return ""
	}
	if mode == "" {
		return ""
	}
	applied, writeErr := s.store.UpdatePrivacyModeIfUnchanged(ctx, observed, mode)
	err = writeErr
	if err == nil && !applied {
		return ""
	}
	if operation == privacyRefresh {
		failure, success := "token_refresh.update_privacy_mode_failed", "token_refresh.privacy_mode_set"
		if platform == PlatformAntigravity {
			failure = "token_refresh.update_antigravity_privacy_mode_failed"
			success = "token_refresh.antigravity_privacy_mode_set"
		}
		if err != nil {
			s.options.Warn(failure, "account_id", value.ID, "error", err)
		} else {
			if platform == PlatformAntigravity {
				ApplyAntigravityPrivacyMode(value, mode)
			}
			s.options.Info(success, "account_id", value.ID, "privacy_mode", mode)
		}
		return mode
	}
	if platform == PlatformOpenAI && operation == privacyEnsure {
		return mode
	}
	if err != nil {
		message := "update_antigravity_privacy_mode_failed: account_id=%d err=%v"
		if platform == PlatformOpenAI {
			message = "force_update_openai_privacy_mode_failed: account_id=%d err=%v"
		} else if operation == privacyForce {
			message = "force_update_antigravity_privacy_mode_failed: account_id=%d err=%v"
		}
		s.options.AdminObserve(message, value.ID, err)
		return mode
	}
	if platform == PlatformAntigravity {
		ApplyAntigravityPrivacyMode(value, mode)
	} else {
		if value.Extra == nil {
			value.Extra = map[string]any{}
		}
		value.Extra["privacy_mode"] = mode
	}
	return mode
}
func (s *PrivacyService) EnsureOpenAIPrivacy(ctx context.Context, v *Record) string {
	return s.apply(ctx, v, PlatformOpenAI, privacyEnsure)
}
func (s *PrivacyService) ForceOpenAIPrivacy(ctx context.Context, v *Record) string {
	return s.apply(ctx, v, PlatformOpenAI, privacyForce)
}
func (s *PrivacyService) EnsureAntigravityPrivacy(ctx context.Context, v *Record) string {
	return s.apply(ctx, v, PlatformAntigravity, privacyEnsure)
}
func (s *PrivacyService) ForceAntigravityPrivacy(ctx context.Context, v *Record) string {
	return s.apply(ctx, v, PlatformAntigravity, privacyForce)
}
func (s *PrivacyService) RefreshOpenAIPrivacy(ctx context.Context, v *Record) {
	s.apply(ctx, v, PlatformOpenAI, privacyRefresh)
}
func (s *PrivacyService) RefreshAntigravityPrivacy(ctx context.Context, v *Record) {
	s.apply(ctx, v, PlatformAntigravity, privacyRefresh)
}

var ErrPrivacyStopped = errors.New("account privacy maintenance stopped")

// StopContext 取消实际隐私请求并等待；已排队的旧任务由 app 完成屏障等待，后续调用不再发起 I/O。
func (s *PrivacyService) StopContext(ctx context.Context) error {
	return s.activity.stop(ctx, "account privacy maintenance")
}
