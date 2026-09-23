package service

import "github.com/TokenFlux/TokenRouter/internal/scheduler"

// BindSchedulerRuntime 在开始请求前绑定同一反馈与参数拥有者。
func (s *GatewayService) BindSchedulerRuntime(feedback *scheduler.RuntimeStats, parameters *scheduler.Parameters) {
	if s == nil {
		return
	}
	s.advancedAccountStats = feedback
	s.schedulerParameters = parameters
}

// BindSchedulerRuntime 在开始请求前绑定同一反馈与参数拥有者。
func (s *OpenAIGatewayService) BindSchedulerRuntime(feedback *scheduler.RuntimeStats, parameters *scheduler.Parameters) {
	if s == nil {
		return
	}
	s.openaiAccountStats = feedback
	s.schedulerParameters = parameters
}

// BindSchedulerRuntime 在开始请求前绑定同一反馈与参数拥有者。
func (s *GeminiMessagesCompatService) BindSchedulerRuntime(feedback *scheduler.RuntimeStats, parameters *scheduler.Parameters) {
	if s == nil {
		return
	}
	s.advancedAccountStats = feedback
	s.schedulerParameters = parameters
}

// BindSchedulerRuntime 在开始请求前绑定同一反馈与参数拥有者。
func (s *AdvancedSchedulerScoreDiagnosticService) BindSchedulerRuntime(feedback *scheduler.RuntimeStats, parameters *scheduler.Parameters) {
	if s == nil {
		return
	}
	s.feedback = feedback
	s.schedulerParameters = parameters
}
