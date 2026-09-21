//go:build unit

package identity_test

// runProfileBackground 保留原测试中异步失效的时机，完成由各用例的同步断言等待。
func runProfileBackground(_ string, task func()) bool {
	go task()
	return true
}

func boolPtr(value bool) *bool { return &value }

func float64Ptr(value float64) *float64 { return &value }
