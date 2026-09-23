package service

// BindBackgroundTasks 在启动前绑定任务拥有者，旧执行入口不再替换进程全局状态。
func (s *OpenAIGatewayService) BindBackgroundTasks(run func(string, func()) bool) {
	s.backgroundTasks = run
}

func (s *GatewayService) BindBackgroundTasks(run func(string, func()) bool) {
	s.backgroundTasks = run
}

// RunBackgroundTask 保留独立构造器的异步语义；生产始终使用 app 的完成屏障。
func (s *OpenAIGatewayService) RunBackgroundTask(name string, fn func()) bool {
	if s != nil && s.backgroundTasks != nil {
		return s.backgroundTasks(name, fn)
	}
	go fn()
	return true
}

func (s *GatewayService) RunBackgroundTask(name string, fn func()) bool {
	if s != nil && s.backgroundTasks != nil {
		return s.backgroundTasks(name, fn)
	}
	go fn()
	return true
}
