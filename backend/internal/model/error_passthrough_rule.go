// 错误规则值类型由 gateway 唯一拥有；旧 model 只保留兼容名称。
package model

import "github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"

type ErrorPassthroughRule = errorpolicy.ErrorPassthroughRule

const MatchModeAny = errorpolicy.MatchModeAny
const MatchModeAll = errorpolicy.MatchModeAll
const PlatformAnthropic = errorpolicy.PlatformAnthropic
const PlatformOpenAI = errorpolicy.PlatformOpenAI
const PlatformGemini = errorpolicy.PlatformGemini
const PlatformAntigravity = errorpolicy.PlatformAntigravity
const PlatformQoder = errorpolicy.PlatformQoder
const PlatformGrok = errorpolicy.PlatformGrok
const PlatformKimi = errorpolicy.PlatformKimi
const PlatformZhipu = errorpolicy.PlatformZhipu
const PlatformDeepseek = errorpolicy.PlatformDeepseek

func AllPlatforms() []string { return errorpolicy.AllPlatforms() }

type ValidationError = errorpolicy.ValidationError
