package domain

import "github.com/TokenFlux/TokenRouter/internal/routing"

// 旧实体与 schema 使用类型别名，纯管理员规则由 routing 拥有。
type ReasoningEffortMapping = routing.ReasoningEffortMapping

const ReasoningEffortMatchExact = routing.ReasoningEffortMatchExact
const ReasoningEffortMatchPrefix = routing.ReasoningEffortMatchPrefix
const ReasoningEffortMatchSuffix = routing.ReasoningEffortMatchSuffix
