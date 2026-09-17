package policy

import "math"

// ConfigScoreWeights 保留进程配置的字段和求和顺序，执行评分视图继续独立投影。
type ConfigScoreWeights struct {
	Priority  float64 `mapstructure:"priority"`
	Load      float64 `mapstructure:"load"`
	Queue     float64 `mapstructure:"queue"`
	ErrorRate float64 `mapstructure:"error_rate"`
	TTFT      float64 `mapstructure:"ttft"`
	// Reset 倾向「会话窗口最早重置」的账号。
	// >0 时，剩余重置时间越短的账号得分越高，从而被优先用尽。默认 0（关闭，不改变原有行为）。
	Reset float64 `mapstructure:"reset"`
	// QuotaHeadroom 倾向 Codex 7d 剩余额度更健康的账号。
	// 默认 0（关闭，不改变原有行为）。
	QuotaHeadroom float64 `mapstructure:"quota_headroom"`
	// PreviousResponse/SessionSticky 仅在高级调度启用粘性加权时生效。
	PreviousResponse float64 `mapstructure:"previous_response"`
	SessionSticky    float64 `mapstructure:"session_sticky"`
}

func (w ConfigScoreWeights) BaseWeightSum() float64 {
	return w.Priority + w.Load + w.Queue + w.ErrorRate + w.TTFT + w.Reset + w.QuotaHeadroom
}

func (w ConfigScoreWeights) TotalWeightSum() float64 {
	return w.BaseWeightSum() + w.PreviousResponse + w.SessionSticky
}

func (w ConfigScoreWeights) IsValid() bool {
	for _, weight := range []float64{
		w.Priority, w.Load, w.Queue, w.ErrorRate, w.TTFT, w.Reset,
		w.QuotaHeadroom, w.PreviousResponse, w.SessionSticky,
	} {
		if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return false
		}
	}
	baseSum := w.BaseWeightSum()
	return baseSum > 0 && !math.IsNaN(baseSum) && !math.IsInf(baseSum, 0) &&
		!math.IsNaN(w.TotalWeightSum()) && !math.IsInf(w.TotalWeightSum(), 0)
}
