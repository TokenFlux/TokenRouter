// Coding Plan 的原窗口投影，百分比不代表货币余额。
package usageview

// CNQuotaTier 表示 Coding Plan 的滚动用量窗口。
type CNQuotaTier struct {
	Window      string  `json:"window"`
	UsedPercent float64 `json:"used_percent"`
	ResetAt     string  `json:"reset_at,omitempty"`
}
