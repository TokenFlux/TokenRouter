package forward

// AgentIdentityTaskRecoveredError 保留本次任务已恢复的重试信号，不代表已重新发送请求。
type AgentIdentityTaskRecoveredError struct{}

func (e *AgentIdentityTaskRecoveredError) Error() string { return "agent identity task recovered" }
