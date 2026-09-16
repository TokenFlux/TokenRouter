package service

import (
	"context"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// TokenRefresher 定义平台特定的token刷新策略接口
// 通过此接口可以扩展支持不同平台（Anthropic/OpenAI/Gemini）
type TokenRefresher interface {
	// CanRefresh 检查此刷新器是否能处理指定账号
	CanRefresh(account *Account) bool

	// NeedsRefresh 检查账号的token是否需要刷新
	NeedsRefresh(account *Account, refreshWindow time.Duration) bool

	// Refresh 执行token刷新，返回更新后的credentials
	// 注意：返回的map应该保留原有credentials中的所有字段，只更新token相关字段
	Refresh(ctx context.Context, account *Account) (map[string]any, error)
}

// ClaudeTokenRefresher 处理Anthropic/Claude OAuth token刷新
type ClaudeTokenRefresher struct {
	oauthService *OAuthService
}

// NewClaudeTokenRefresher 创建Claude token刷新器
func NewClaudeTokenRefresher(oauthService *OAuthService) *ClaudeTokenRefresher {
	return &ClaudeTokenRefresher{
		oauthService: oauthService,
	}
}

// CacheKey 返回用于分布式锁的缓存键
func (r *ClaudeTokenRefresher) CacheKey(account *Account) string {
	return ClaudeTokenCacheKey(account)
}

func (r *ClaudeTokenRefresher) CanRefresh(account *Account) bool {
	return accountcore.CanRefreshClaude(AccountRecordView(account))
}

func (r *ClaudeTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	return accountcore.NeedsRefreshClaude(AccountRecordView(account), refreshWindow)
}

func (r *ClaudeTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	return accountcore.RefreshClaudeCredentials(ctx, AccountRecordView(account), r.oauthService.ClaudeAuthorization.RefreshAccountToken)
}

// OpenAITokenRefresher 处理 OpenAI OAuth token刷新
type OpenAITokenRefresher struct {
	openaiOAuthService *OpenAIOAuthService
	accountRepo        AccountRepository
}

// NewOpenAITokenRefresher 创建 OpenAI token刷新器
func NewOpenAITokenRefresher(openaiOAuthService *OpenAIOAuthService, accountRepo AccountRepository) *OpenAITokenRefresher {
	return &OpenAITokenRefresher{
		openaiOAuthService: openaiOAuthService,
		accountRepo:        accountRepo,
	}
}

func (r *OpenAITokenRefresher) CacheKey(account *Account) string {
	return r.openAICore().CacheKey(AccountRecordView(account))
}

func (r *OpenAITokenRefresher) CanRefresh(account *Account) bool {
	return r.openAICore().CanRefresh(AccountRecordView(account))
}

func (r *OpenAITokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	return r.openAICore().NeedsRefresh(AccountRecordView(account), refreshWindow)
}

func (r *OpenAITokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	return r.openAICore().Refresh(ctx, AccountRecordView(account))
}

// 旧刷新器只投影账号，不复制凭据合并规则或运行资源。
func (r *OpenAITokenRefresher) openAICore() *accountcore.OpenAITokenRefresher {
	return &accountcore.OpenAITokenRefresher{Authorization: r.openaiOAuthService.Core()}
}
