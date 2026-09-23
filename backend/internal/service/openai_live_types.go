package service

import gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

// LiveCallCreated 表示上游成功创建的 Live 会话。
type LiveCallCreated struct {
	SDP      []byte
	CallID   string
	Location string
	Account  *gatewayprovider.

		// Live 并发租约契约由 scheduler 拥有；远端会话记录仍留执行层。
		ExecutionAccount
}
