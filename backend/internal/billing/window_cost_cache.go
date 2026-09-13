// WindowCostCache 拥有账号资金窗口的只读缓存契约，保留原聚合口径与 TTL。
package billing

import "context"

type WindowCostCache interface {

	// ========== 5h窗口费用缓存 ==========
	// Key 格式: window_cost:account:{accountID}
	// 用于缓存账号在当前5h窗口内的标准费用，减少数据库聚合查询压力

	// GetWindowCost 获取缓存的窗口费用
	// 返回 (cost, true, nil) 如果缓存命中
	// 返回 (0, false, nil) 如果缓存未命中
	// 返回 (0, false, err) 如果发生错误
	GetWindowCost(ctx context.Context, accountID int64) (cost float64, hit bool, err error)

	// SetWindowCost 设置窗口费用缓存
	SetWindowCost(ctx context.Context, accountID int64, cost float64) error

	// GetWindowCostBatch 批量获取窗口费用缓存
	// 返回 map[accountID]cost，缓存未命中的账号不在 map 中
	GetWindowCostBatch(ctx context.Context, accountIDs []int64) (map[int64]float64, error)
}
