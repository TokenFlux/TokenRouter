// Package logevent 只定义不可变发布的技术日志事件，不认识业务表。
package logevent

import "time"

const OpsSystemLogSkipField = "ops_system_log_skip"

type LogEvent struct {
	Time       time.Time
	Level      string
	Component  string
	Message    string
	LoggerName string
	Fields     map[string]any
}
