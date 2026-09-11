package service

import "testing"

// 时间轮初始化失败已在目标包 Start 契约与 lifecycle 回收测试覆盖。
func TestProvideTimingWheelService_Success(t *testing.T) {
	svc, err := ProvideTimingWheelService()
	if err != nil || svc == nil {
		t.Fatalf("构造失败：%v", err)
	}
	svc.Stop()
}
