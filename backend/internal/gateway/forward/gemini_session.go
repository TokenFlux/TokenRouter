package forward

// GeminiSession 固化执行入口提供的会话归属，不负责选号或创建远端会话。
type GeminiSession struct {
	GroupID     int64
	SessionHash string
}

// GeminiSessionOption 保留可省略、nil 和按顺序覆盖的原调用约定。
type GeminiSessionOption func(*GeminiSession)

func WithGeminiSession(groupID int64, sessionHash string) GeminiSessionOption {
	return func(options *GeminiSession) { options.GroupID = groupID; options.SessionHash = sessionHash }
}
