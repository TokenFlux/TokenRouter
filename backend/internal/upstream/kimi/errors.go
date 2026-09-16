// 并发文案要求精确匹配，其他 CN 平台的权限错误不能继承此分类。
package kimi

import "strings"

const ConcurrentRequestLimitMessage = "You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."

func IsConcurrencyLimitMessage(message string) bool {
	return strings.TrimSpace(message) == ConcurrentRequestLimitMessage
}
