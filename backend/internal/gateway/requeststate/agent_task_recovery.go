package requeststate

import "context"

type agentTaskRecoveryKey struct{}

// WithAgentTaskRecovery 只标记当前尝试链已使用恢复机会，不修改账号或持久状态。
func WithAgentTaskRecovery(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentTaskRecoveryKey{}, true)
}
func AgentTaskRecoveryTried(ctx context.Context) bool {
	value, _ := ctx.Value(agentTaskRecoveryKey{}).(bool)
	return value
}
