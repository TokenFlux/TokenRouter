// QuotaDimension 只表达通知展示所需的已确定窗口值。
package contract

type QuotaDimension struct {
	Name               string
	Enabled            bool
	Threshold          float64
	ThresholdType      string
	CurrentUsed, Limit float64
}
