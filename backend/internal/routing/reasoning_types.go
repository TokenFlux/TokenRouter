package routing

import "github.com/TokenFlux/TokenRouter/internal/routing/accessview"

// ReasoningEffortMapping 在应用分组上限前，将一个显式的 OpenAI/Codex
// 推理强度值改写为另一个值。
const (
	ReasoningEffortMatchExact  = "exact"
	ReasoningEffortMatchPrefix = "prefix"
	ReasoningEffortMatchSuffix = "suffix"
)

type ReasoningEffortMapping = accessview.ReasoningEffortMapping
