// 旧刷新器保留原接口，通过账号投影委托唯一窗口与合并实现。
package service

import (
	"context"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

type GrokTokenRefresher struct {
	core *accountcore.GrokTokenRefresher
}
type grokRefreshTokenBridge struct{ source GrokOAuthTokenService }

func (b grokRefreshTokenBridge) RefreshAccountToken(ctx context.Context, value *accountcore.Record) (*accountcore.GrokTokenInfo, error) {
	return b.source.RefreshAccountToken(ctx, AccountFromRecord(value))
}
func (b grokRefreshTokenBridge) BuildAccountCredentials(info *accountcore.GrokTokenInfo) map[string]any {
	return b.source.BuildAccountCredentials(info)
}
func NewGrokTokenRefresher(source GrokOAuthTokenService) *GrokTokenRefresher {
	var port accountcore.GrokRefreshTokenService
	if source != nil {
		port = grokRefreshTokenBridge{source}
	}
	return &GrokTokenRefresher{core: accountcore.NewGrokTokenRefresher(port)}
}

const grokTokenRefreshSkew = accountcore.GrokTokenRefreshSkew

func (r *GrokTokenRefresher) CacheKey(account *Account) string {
	return r.native().CacheKey(AccountRecordView(account))
}
func (r *GrokTokenRefresher) CanRefresh(account *Account) bool {
	return r.native().CanRefresh(AccountRecordView(account))
}
func (r *GrokTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	return r.native().NeedsRefresh(AccountRecordView(account), refreshWindow)
}

func (r *GrokTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	return r.native().Refresh(ctx, AccountRecordView(account))
}

// nil 接收者沿用旧刷新器自身的判断，不在兼容层提前解引用。
func (r *GrokTokenRefresher) native() *accountcore.GrokTokenRefresher {
	if r == nil {
		return nil
	}
	return r.core
}
