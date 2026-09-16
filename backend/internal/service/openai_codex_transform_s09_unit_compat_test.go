//go:build unit

// 原行为测试继续经过相同转接；生产消费者清零后仅保留 unit 兼容。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type codexInputFilterOptions = native.CodexInputFilterOptions

func filterCodexInputWithOptions(input []any, opts codexInputFilterOptions) []any {
	return native.FilterCodexInputWithOptions(input, opts)
}
