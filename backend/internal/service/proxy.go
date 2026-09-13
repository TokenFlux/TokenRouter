// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
)

const FallbackModeNone = egress.FallbackModeNone

const FallbackModeProxy = egress.FallbackModeProxy

const FallbackModeDirect = egress.FallbackModeDirect

type Proxy = egress.Proxy

type ProxyWithAccountCount = egress.ProxyWithAccountCount

type ProxyAccountSummary = egress.ProxyAccountSummary
