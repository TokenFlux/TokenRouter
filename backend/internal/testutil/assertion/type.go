// Package assertion 提供不依赖业务实体的测试值校验。
package assertion

import "fmt"

// MustType 检查测试替身或解码结果的类型；不符合时直接令当前测试失败。
// 与直接类型断言一样保留 panic 语义，消息仅包含类型，不暴露测试值中的凭据。
func MustType[T any](value any) T {
	result, ok := value.(T)
	if !ok {
		var expected T
		panic(fmt.Sprintf("expected %T, got %T", expected, value))
	}
	return result
}
