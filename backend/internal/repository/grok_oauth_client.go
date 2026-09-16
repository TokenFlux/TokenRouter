// 旧 Wire 构造入口返回唯一供应商客户端，S15/S16 清理。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func NewGrokOAuthClient() service.GrokOAuthClient { return grok.NewOAuthClient() }
