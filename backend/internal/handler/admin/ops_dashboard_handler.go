// 旧 HTTP 入口只委托新 Adapter，S15/S16 清理。
package admin

import (
	time "time"

	native "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
)

func pickThroughputBucketSeconds(window time.Duration) int {
	return native.CompatPickThroughputBucketSeconds(window)
}
