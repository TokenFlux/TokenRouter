// 兼容构造只读取技术参数，writer/队列/退避由 ops 唯一持有。
package service

import (
	"fmt"
	"os"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
)

type OpsSystemLogSink = ops.OpsSystemLogSink
type OpsSystemLogSinkHealth = ops.OpsSystemLogSinkHealth

func NewOpsSystemLogSink(repo OpsRepository) *OpsSystemLogSink {
	host, e := os.Hostname()
	return ops.NewOpsSystemLogSink(repo, ops.SystemLogSinkOptions{Host: host, HostError: e, OnWriteFailure: func(err error, batch, failures int, backoff time.Duration) {
		_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"ops system log sink flush failed\" err=%v batch=%d failures=%d backoff=%s\n", time.Now().Format(time.RFC3339Nano), err, batch, failures, backoff)
	}})
}
