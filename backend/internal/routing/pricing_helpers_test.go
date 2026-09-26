//go:build unit

package routing

// 测试直接给出价卡可空金额，沿用原断言输入。
func testPtrFloat64(value float64) *float64 { return &value }
func testPtrInt(value int) *int             { return &value }

func testPtrString(value string) *string { return &value }
