//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package handler

import (
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
)

const wechatOAuthIntentBind = identityhttp.WechatOAuthIntentBind

func wechatSyntheticEmail(subject string) string { return identitycore.WeChatSyntheticEmail(subject) }
