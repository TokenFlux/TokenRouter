// 原构造入口只返回唯一平台客户端，不持有网络算法或状态。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/service"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

type claudeOAuthService = native.OAuthClient

func NewClaudeOAuthClient() service.ClaudeOAuthClient { return native.NewOAuthClient() }
