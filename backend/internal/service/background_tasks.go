package service

import "sync"

// BackgroundTaskRunner 是旧调用方需要的完成跟踪端口，不拥有业务状态。
// app 注入实际生命周期实现；S04—S14 随调用方迁移，S16 删除旧默认入口。
type BackgroundTaskRunner interface{ Go(string, func()) bool }

var backgroundRunner struct {
	sync.RWMutex
	value BackgroundTaskRunner
}

// SetBackgroundTaskRunner 由组合根绑定；返回函数用于失败回收和进程内测试隔离。
func SetBackgroundTaskRunner(runner BackgroundTaskRunner) func() {
	backgroundRunner.Lock()
	previous := backgroundRunner.value
	backgroundRunner.value = runner
	backgroundRunner.Unlock()
	return func() { backgroundRunner.Lock(); backgroundRunner.value = previous; backgroundRunner.Unlock() }
}

// RunBackgroundTask 保留独立旧调用方的 go 语义；生产路径始终注入跟踪器。
func RunBackgroundTask(name string, fn func()) bool {
	backgroundRunner.RLock()
	runner := backgroundRunner.value
	backgroundRunner.RUnlock()
	if runner != nil {
		return runner.Go(name, fn)
	}
	go fn()
	return true
}

// 绑定器在派发前求值参数，避免闭包捕获改变原 go 语句的调用对象或循环变量。
func BackgroundCall0(fn func()) func()                         { return fn }
func BackgroundCall1[A any](fn func(A), a A) func()            { return func() { fn(a) } }
func BackgroundCall2[A, B any](fn func(A, B), a A, b B) func() { return func() { fn(a, b) } }
func BackgroundCall3[A, B, C any](fn func(A, B, C), a A, b B, c C) func() {
	return func() { fn(a, b, c) }
}
