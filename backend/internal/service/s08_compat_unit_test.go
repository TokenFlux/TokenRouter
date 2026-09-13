//go:build unit

// 这些旧测试入口只委托新实现；生产已无消费者。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

func boolPtr(v bool) *bool          { return ops.CompatBoolPtr(v) }
func float64Ptr(v float64) *float64 { return ops.CompatFloat64Ptr(v) }

// 其他旧模块测试继续复用原标量构造，不复制 Ops 实现。
func int64Ptr(v int64) *int64 { return &v }
func strPtr(v string) *string { return &v }
