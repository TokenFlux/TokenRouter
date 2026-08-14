package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCalculateSearchCost(t *testing.T) {
	t.Parallel()
	s := &BillingService{}
	require.Equal(t, 0.0, s.CalculateSearchCost(0, floatPtr(10), 1).ActualCost)
	// 价格为 nil 时采用 xAI 官方默认每千次 5 美元，5 次调用费用为 0.025 美元。
	require.InDelta(t, 0.025, s.CalculateSearchCost(5, nil, 1).ActualCost, 1e-9)
	// 显式配置为零表示免费。
	require.Equal(t, 0.0, s.CalculateSearchCost(5, floatPtr(0), 1).ActualCost)
	price := 10.0
	cost := s.CalculateSearchCost(100, &price, 1.5)
	// 计算方式为 10 / 1000 * 100 * 1.5 = 1.5。
	require.InDelta(t, 1.0, cost.TotalCost, 1e-9)
	require.InDelta(t, 1.5, cost.ActualCost, 1e-9)
}

func TestCalculateAudioCost(t *testing.T) {
	t.Parallel()
	s := &BillingService{}
	rt, tts, stt := 0.10, 15.0, 0.50
	cfg := &audioPriceConfig{RealtimePerMin: &rt, TTSPerMChars: &tts, STTPerHour: &stt}
	require.InDelta(t, 0.20, s.CalculateAudioCost("realtime", 2, cfg, 1).ActualCost, 1e-9)
	require.InDelta(t, 1.5, s.CalculateAudioCost("tts", 0.1, cfg, 1).ActualCost, 1e-9)
	require.InDelta(t, 0.25, s.CalculateAudioCost("stt", 0.5, cfg, 1).ActualCost, 1e-9)
	require.Equal(t, 0.0, s.CalculateAudioCost("unknown", 1, cfg, 1).ActualCost)
	// 配置为 nil 时采用官方默认价：think-fast-1 每分钟 0.05 美元、TTS 每百万字符 15 美元、REST STT 每小时 0.10 美元。
	require.InDelta(t, 0.05, s.CalculateAudioCost("realtime", 1, nil, 1).ActualCost, 1e-9)
	require.InDelta(t, 15.0, s.CalculateAudioCost("tts", 1, nil, 1).ActualCost, 1e-9)
	require.InDelta(t, 0.10, s.CalculateAudioCost("stt", 1, nil, 1).ActualCost, 1e-9)
	// 显式配置为零表示免费。
	zero := 0.0
	require.Equal(t, 0.0, s.CalculateAudioCost("realtime", 1, &audioPriceConfig{RealtimePerMin: &zero}, 1).ActualCost)
}

func floatPtr(v float64) *float64 { return &v }
