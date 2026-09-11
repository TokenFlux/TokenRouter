package service

import "github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"

// TimingWheelService 保留旧业务消费者的类型入口，S06/S08 清理。
type TimingWheelService = timingwheel.Wheel

// NewTimingWheelService 仅构造，生产图统一由 app 启动。
func NewTimingWheelService() (*TimingWheelService, error) { return timingwheel.New(), nil }
