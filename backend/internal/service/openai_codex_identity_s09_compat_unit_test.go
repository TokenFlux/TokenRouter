//go:build unit

// 原 unit 断言使用的常量只保留测试转接，不留生产重复声明。
package service

import (
	openai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

const codexUpstreamMinVersion = openai.CodexUpstreamMinVersion
