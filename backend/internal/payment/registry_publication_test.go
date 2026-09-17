package payment

import "testing"

// 构造候选期间暂停，确认读取者始终看到上一次完整发布。
type blockedProvider struct {
	mockProvider
	entered, release chan struct{}
}

func (p *blockedProvider) SupportedTypes() []PaymentType {
	close(p.entered)
	<-p.release
	return p.supportedTypes
}
func TestRegistryReplacePublishesOnlyCompleteMap(t *testing.T) {
	registry := NewRegistry()
	old := &mockProvider{name: "old", supportedTypes: []string{TypeAlipay, TypeWxpay}}
	registry.Register(old)
	next := &mockProvider{name: "new-a", supportedTypes: []string{TypeAlipay}}
	blocked := &blockedProvider{
		mockProvider: mockProvider{name: "new-b", supportedTypes: []string{TypeWxpay}},
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	done := make(chan struct{})
	go func() { registry.Replace([]Provider{next, blocked}); close(done) }()
	<-blocked.entered
	a, ea := registry.GetProvider(TypeAlipay)
	b, eb := registry.GetProvider(TypeWxpay)
	close(blocked.release)
	<-done
	if ea != nil || eb != nil || a != old || b != old {
		t.Fatal("候选未就绪时不能发布空表或半张表")
	}
	a, ea = registry.GetProvider(TypeAlipay)
	b, eb = registry.GetProvider(TypeWxpay)
	if ea != nil || eb != nil || a != next || b != blocked {
		t.Fatal("发布后必须包含完整候选")
	}
}
