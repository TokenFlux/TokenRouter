// 兼容任务目录入口，模型候选由上游纯目录唯一持有。
package batchimage

import "github.com/TokenFlux/TokenRouter/internal/upstream"

func DefaultBatchImageModelCandidates() []string { return upstream.DefaultImageTaskGeminiModels() }
