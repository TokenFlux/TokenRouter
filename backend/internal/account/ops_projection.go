// LoadObservation 只投影观测所需账号 ID 与有效负载上限。
package account

type LoadObservation struct {
	ID             int64
	MaxConcurrency int
}
