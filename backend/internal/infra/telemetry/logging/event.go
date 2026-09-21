package logging

import "go.uber.org/zap"

// Event 把交替键值形式的观测字段写入唯一日志后端；非法键和末尾孤立值保持忽略。
// 原调度观察端口只使用 error/warn，其余等级继续按 debug 写入。
func Event(level, event string, fields ...any) {
	values := make([]zap.Field, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if ok {
			values = append(values, zap.Any(key, fields[i+1]))
		}
	}
	switch level {
	case "error":
		L().Error(event, values...)
	case "warn":
		L().Warn(event, values...)
	default:
		L().Debug(event, values...)
	}
}
