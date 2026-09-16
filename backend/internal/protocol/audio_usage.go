// 音频计量只描述已确认的单位；价格、资金与去重由账单拥有。
package protocol

// ForwardResult 转发结果
type AudioUsage struct {
	Mode            string  // realtime | tts | stt
	DurationOrUnits float64 // minutes / million-chars / hours
}
