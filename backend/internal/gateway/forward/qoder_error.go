package forward

// QoderErrorView 是单次供应商错误的展示投影，不携带账号、凭据或可变服务。
type QoderErrorView struct {
	Recognized           bool
	Status, SourceStatus int
	Kind, Message, Body  string
}
