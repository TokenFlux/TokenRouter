// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

// 代理状态值沿用原存储格式。
const (
	StatusActive  = "active"
	StatusExpired = "expired"
)

// Diagnostics 由装配注入原日志出口，核心不安装或持有日志后端。
type Diagnostics struct {
	Logf func(component, format string, args ...any)
}

func (d Diagnostics) Log(component, format string, args ...any) {
	if d.Logf != nil {
		d.Logf(component, format, args...)
	}
}
func diagnosticsOption(options []Diagnostics) Diagnostics {
	if len(options) > 0 {
		return options[0]
	}
	return Diagnostics{}
}
