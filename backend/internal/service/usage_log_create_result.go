// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/usage"

type UsageLogCreateError = native.UsageLogCreateError

func MarkUsageLogCreateNotPersisted(err error) error {
	return native.MarkUsageLogCreateNotPersisted(err)
}
func MarkUsageLogCreateDropped(err error) error   { return native.MarkUsageLogCreateDropped(err) }
func IsUsageLogCreateNotPersisted(err error) bool { return native.IsUsageLogCreateNotPersisted(err) }
func IsUsageLogCreateDropped(err error) bool      { return native.IsUsageLogCreateDropped(err) }
func ShouldBillAfterUsageLogCreate(inserted bool, err error) bool {
	return native.ShouldBillAfterUsageLogCreate(inserted, err)
}
