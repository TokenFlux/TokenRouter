package policy

// 消息串行和节流模式保持原配置字符串，策略值不依赖配置装配。
const (
	MessageQueueSerialize = "serialize"
	MessageQueueThrottle  = "throttle"
)
