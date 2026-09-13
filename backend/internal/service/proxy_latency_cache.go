// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
)

type ProxyLatencyInfo = egress.ProxyLatencyInfo

type ProxyLatencyCache = egress.ProxyLatencyCache
