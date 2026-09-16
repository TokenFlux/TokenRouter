package text

// SingleCountPorts 保留只选择一次的计数入口；没有并发槽、会话登记或完成提交能力。
// 具体账号与上游资源由 Adapter 持有，核心只接收明确的可用性与错误结果。
type SingleCountPorts interface {
	Select() (bool, error)
	Selected()
	SelectionFailed(error)
	Forward() error
	ForwardFailed(error)
}

// RunSingleCountTokens 不给原本无切号的 OpenAI 计数入口添加重试或费用路径。
func RunSingleCountTokens(p SingleCountPorts) {
	available, err := p.Select()
	p.Selected()
	if err != nil || !available {
		p.SelectionFailed(err)
		return
	}
	if err := p.Forward(); err != nil {
		p.ForwardFailed(err)
	}
}
