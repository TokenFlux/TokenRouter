//go:build unit

// 保留原标签测试的私有委托，生产代码不保留测试专用入口。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/search"
)

func validateWebSearchConfig(c *WebSearchEmulationConfig) error { return search.ValidateConfig(c) }

func parseWebSearchConfigJSON(raw string) *WebSearchEmulationConfig { return search.ParseConfig(raw) }
