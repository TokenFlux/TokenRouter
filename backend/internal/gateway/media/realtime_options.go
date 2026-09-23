package media

import "time"

// DefaultRealtimeDialTimeout 只限制下游升级前的上游握手，连接建立后不终止会话。
const DefaultRealtimeDialTimeout = 12 * time.Second
