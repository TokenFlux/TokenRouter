// 账号五小时窗口费用缓存保持原 key、三十秒 TTL 与浮点读取语义。
package rediscache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	windowCostKeyPrefix = "window_cost:account:"
	windowCostCacheTTL  = 30 * time.Second
)

type WindowCostCache struct{ rdb *redis.Client }

func NewWindowCostCache(rdb *redis.Client) *WindowCostCache { return &WindowCostCache{rdb: rdb} }
func windowCostKey(id int64) string                         { return fmt.Sprintf("%s%d", windowCostKeyPrefix, id) }

// ========== 5h窗口费用缓存实现 ==========

// GetWindowCost 获取缓存的窗口费用
func (c *WindowCostCache) GetWindowCost(ctx context.Context, accountID int64) (float64, bool, error) {
	key := windowCostKey(accountID)
	val, err := c.rdb.Get(ctx, key).Float64()
	if err == redis.Nil {
		return 0, false, nil // 缓存未命中
	}
	if err != nil {
		return 0, false, err
	}
	return val, true, nil
}

// SetWindowCost 设置窗口费用缓存
func (c *WindowCostCache) SetWindowCost(ctx context.Context, accountID int64, cost float64) error {
	key := windowCostKey(accountID)
	return c.rdb.Set(ctx, key, cost, windowCostCacheTTL).Err()
}

// GetWindowCostBatch 批量获取窗口费用缓存
func (c *WindowCostCache) GetWindowCostBatch(ctx context.Context, accountIDs []int64) (map[int64]float64, error) {
	if len(accountIDs) == 0 {
		return make(map[int64]float64), nil
	}

	// 构建批量查询的 keys
	keys := make([]string, len(accountIDs))
	for i, accountID := range accountIDs {
		keys[i] = windowCostKey(accountID)
	}

	// 使用 MGET 批量获取
	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	results := make(map[int64]float64, len(accountIDs))
	for i, val := range vals {
		if val == nil {
			continue // 缓存未命中
		}
		// 尝试解析为 float64
		switch v := val.(type) {
		case string:
			if cost, err := strconv.ParseFloat(v, 64); err == nil {
				results[accountIDs[i]] = cost
			}
		case float64:
			results[accountIDs[i]] = v
		}
	}

	return results, nil
}
