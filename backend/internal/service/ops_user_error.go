// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/ops"

type UserErrorRequest = native.UserErrorRequest
type UserErrorRequestList = native.UserErrorRequestList

func MapUserErrorCategory(phase, errType string) string {
	return native.MapUserErrorCategory(phase, errType)
}
func CategoryToFilter(category string) (phases []string, errorTypes []string) {
	return native.CategoryToFilter(category)
}
func ToUserErrorRequest(e *OpsErrorLog) *UserErrorRequest { return native.ToUserErrorRequest(e) }

type UserErrorRequestDetail = native.UserErrorRequestDetail

func ToUserErrorRequestDetail(e *OpsErrorLogDetail) *UserErrorRequestDetail {
	return native.ToUserErrorRequestDetail(e)
}
