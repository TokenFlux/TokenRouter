package handler

import "github.com/TokenFlux/TokenRouter/internal/scheduler"

// 图片入口保留类型名，独立本地容量与释放契约由 scheduler 唯一持有。
type imageConcurrencyLimiter = scheduler.ImageConcurrencyLimiter
