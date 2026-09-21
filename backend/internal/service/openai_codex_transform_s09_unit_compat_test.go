//go:build unit

// 原行为测试继续经过相同转接；生产消费者清零后仅保留 unit 兼容。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type codexInputFilterOptions = openai.CodexInputFilterOptions

func filterCodexInputWithOptions(input []any, opts codexInputFilterOptions) []any {
	return openai.FilterCodexInputWithOptions(input, opts)
}
