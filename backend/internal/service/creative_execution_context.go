package service

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
)

// CreativeExecution 是一次已经完成账号调度与账号槽位预占的执行上下文。
// worker 在标记任务 running 前创建它，确保并发未准入的任务仍保持 queued。
type CreativeExecution struct {
	Native        *creative.CreativeExecution
	Account       *Account
	UpstreamModel string
	Selection     *AccountSelectionResult
	ReleaseFunc   func()
}
