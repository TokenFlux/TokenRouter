//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/creative"
)

func creativeDerefString(v *string) string { return native.CreativeDerefString(v) }
func creativeDerefInt64(v *int64) int64    { return native.CreativeDerefInt64(v) }
