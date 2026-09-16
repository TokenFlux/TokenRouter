// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

type ToolCorrectionStats = native.ToolCorrectionStats
type CodexToolCorrector = native.CodexToolCorrector

func NewCodexToolCorrector() *CodexToolCorrector { return native.NewCodexToolCorrector() }
func CorrectToolName(name string) (string, bool) { return native.CorrectToolName(name) }
func GetToolNameMapping() map[string]string      { return native.GetToolNameMapping() }
