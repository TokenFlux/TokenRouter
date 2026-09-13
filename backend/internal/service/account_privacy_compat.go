// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	"fmt"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	slog "log/slog"
)

// LegacyPrivacyOptions 只调用原平台交换器；S09 改绑具体平台实现。
func LegacyPrivacyOptions(factory PrivacyClientFactory) acctcore.PrivacyOptions {
	options := acctcore.PrivacyOptions{Antigravity: setAntigravityPrivacy, Warn: slog.Warn, Info: slog.Info, AdminObserve: func(format string, args ...any) { logger.LegacyPrintf("service.admin", format, args...) }}
	if factory != nil {
		options.OpenAI = func(ctx context.Context, token, proxy string) string {
			return disableOpenAITraining(ctx, factory, token, proxy)
		}
	}
	return options
}
func legacyAccountPrivacy(repo AccountRepository, proxies ProxyRepository, factory PrivacyClientFactory) *acctcore.PrivacyService {
	return acctcore.NewPrivacyService(legacyPrivacyWriter{repo}, proxies, LegacyPrivacyOptions(factory))
}
func privacyRecord(value *Account) *acctcore.Record {
	if value == nil {
		return nil
	}
	return AccountRecordView(value)
}
func (s *TokenRefreshService) SetAccountPrivacy(privacy *acctcore.PrivacyService) {
	s.accountPrivacy = privacy
}
func (s *TokenRefreshService) privacyService() *acctcore.PrivacyService {
	if s.accountPrivacy != nil {
		return s.accountPrivacy
	}
	return legacyAccountPrivacy(s.accountRepo, s.proxyRepo, s.privacyClientFactory)
}

// legacyPrivacyWriter 不为缺失条件写入能力的旧构造退回无保护覆盖。
type legacyPrivacyWriter struct{ source AccountRepository }

func (w legacyPrivacyWriter) UpdatePrivacyModeIfUnchanged(ctx context.Context, v acctcore.UsageObservationVersion, mode string) (bool, error) {
	writer, ok := w.source.(acctcore.PrivacyStore)
	if !ok {
		return false, fmt.Errorf("privacy conditional writer is not configured")
	}
	return writer.UpdatePrivacyModeIfUnchanged(ctx, v, mode)
}
