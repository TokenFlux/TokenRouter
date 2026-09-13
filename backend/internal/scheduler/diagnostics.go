package scheduler

// Diagnostics 由装配注入唯一日志后端，核心不安装或持有技术日志实例。
type Diagnostics struct {
	Event func(level, event string, fields ...any)
	Logf  func(scope, format string, args ...any)
}

func (d Diagnostics) printf(scope, format string, args ...any) {
	if d.Logf != nil {
		d.Logf(scope, format, args...)
	}
}

func (d Diagnostics) event(level, event string, fields ...any) {
	if d.Event != nil {
		d.Event(level, event, fields...)
	}
}
