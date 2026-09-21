//go:build unit

// 测试标量指针用于区分显式零值与省略值，不依赖业务模块。
package service

func float64Ptr(v float64) *float64 { return &v }

func strPtr(v string) *string { return &v }
