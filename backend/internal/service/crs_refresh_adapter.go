package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
)

// CoordinatedCRSRefresh 仅选择旧平台的交换能力与既有缓存键；协调和条件写由账号核心负责。
func (s *CRSSyncService) CoordinatedCRSRefresh(core *account.OAuthRefreshAPI) func(context.Context, *account.Record) error {
	return func(ctx context.Context, v *account.Record) error {
		old := AccountFromRecord(v)
		var key string
		switch v.Platform {
		case PlatformAnthropic:
			if s.oauthService == nil {
				return nil
			}
			key = ClaudeTokenCacheKey(old)
		case PlatformOpenAI:
			if s.openaiOAuthService == nil {
				return nil
			}
			key = OpenAITokenCacheKey(old)
		case PlatformGemini:
			if s.geminiOAuthService == nil {
				return nil
			}
			key = GeminiTokenCacheKey(old)
		default:
			return nil
		}
		return core.RefreshImported(ctx, v, key, s.RefreshCRSAccount)
	}
}
