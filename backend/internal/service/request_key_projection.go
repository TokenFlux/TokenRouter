package service

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// 只读取旧执行入口已认证的 Key 与当前分组，不承担图片策略。
func getAPIKeyFromContext(c interface{ Get(string) (any, bool) }) *apikey.APIKey {
	if c == nil {
		return nil
	}
	v, exists := c.Get("api_key")
	if !exists {
		return nil
	}
	apiKey, _ := v.(*apikey.APIKey)
	return apiKey
}

func apiKeyGroup(apiKey *apikey.APIKey) *routing.Group {
	if apiKey == nil {
		return nil
	}
	return apiKey.Group
}
