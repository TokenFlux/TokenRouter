// 本文件维护 openai_compat 的所属能力；兼容入口复用唯一实现。
package openai_compat

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

type TextRouteMode = acctcore.TextRouteMode

const TextRouteModePreserveClientProtocol = acctcore.TextRouteModePreserveClientProtocol

const TextRouteModeForceResponses = acctcore.TextRouteModeForceResponses

const TextRouteModeForceChatCompletions = acctcore.TextRouteModeForceChatCompletions

type TextProtocol = acctcore.TextProtocol

const TextProtocolChatCompletions = acctcore.TextProtocolChatCompletions

const TextProtocolResponses = acctcore.TextProtocolResponses

const ExtraKeyTextRouteMode = acctcore.ExtraKeyTextRouteMode

const ExtraKeyResponsesContinuationSupported = acctcore.ExtraKeyResponsesContinuationSupported

// NormalizeTextRouteMode 委托所属模块的唯一实现。
func NormalizeTextRouteMode(mode string) TextRouteMode { return acctcore.NormalizeTextRouteMode(mode) }

// ResolveTextRouteMode 委托所属模块的唯一实现。
func ResolveTextRouteMode(extra map[string]any) TextRouteMode {
	return acctcore.ResolveTextRouteMode(extra)
}

// ResolveResponsesContinuationSupported 委托所属模块的唯一实现。
func ResolveResponsesContinuationSupported(extra map[string]any) bool {
	return acctcore.ResolveResponsesContinuationSupported(extra)
}

// ResolveUpstreamTextProtocol 委托所属模块的唯一实现。
func ResolveUpstreamTextProtocol(extra map[string]any, preferred TextProtocol) TextProtocol {
	return acctcore.ResolveUpstreamTextProtocol(extra, preferred)
}
