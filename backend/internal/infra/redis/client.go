// 本文件构造 Redis 技术客户端；连接配置与故障策略由外层提供。
package redis

import "github.com/redis/go-redis/v9"

// NewClient 按给定选项创建客户端，并按启动配置添加唯一的 timing hook。
func NewClient(options *redis.Options, enableTiming bool) *redis.Client {
	client := redis.NewClient(options)
	if enableTiming {
		client.AddHook(TimingHook{})
	}
	return client
}
