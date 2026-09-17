// Runtime 组合固定的支付用例实例，不复制状态或业务算法。
package payment

type Runtime struct {
	*Checkout
	*OrderQueries
	*RefundWorkflow
	*OrderLifecycle
	*ProviderBindings
}

// ResumeService 向应用 HTTP 装配提供同一个签名实例，不重新读取配置。
func (r *Runtime) ResumeService() *PaymentResumeService { return r.OrderLifecycle.resume }
