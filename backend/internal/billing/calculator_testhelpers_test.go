//go:build unit

package billing_test

// testPtrFloat64 returns a pointer to the given float64 value.
func testPtrFloat64(v float64) *float64 { return &v }

// testPtrInt 表达区间的显式整数边界。
func testPtrInt(v int) *int { return &v }
