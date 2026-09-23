package forward

import "errors"

// ErrCyberPolicyForwarded 表示拒绝已按当前端点输出；调用方进入错误收尾，不换号或重复写出。
var ErrCyberPolicyForwarded = errors.New("openai cyber_policy forwarded to client")
