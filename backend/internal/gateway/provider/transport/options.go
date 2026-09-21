package transport

// Options 仅包含传输所需参数，由组合根按原读取时点提供。
// nil 与显式零值的差异保留在参数解析中。
type Options struct {
	ValidateResolvedIP          bool
	ConnectionPoolIsolation     string
	MaxUpstreamClients          int
	ClientIdleTTLSeconds        int
	MaxIdleConns                int
	MaxIdleConnsPerHost         int
	MaxConnsPerHost             int
	IdleConnTimeoutSeconds      int
	ResponseHeaderTimeout       int
	OpenAIResponseHeaderTimeout int
	GrokResponseHeaderTimeout   int
	OpenAIHTTP2                 HTTP2Options
}

// HTTP2Options 保留开关、阈值及秒级时间配置，回退状态由 egress 持有。
type HTTP2Options struct {
	Enabled                   bool
	AllowProxyFallbackToHTTP1 bool
	FallbackErrorThreshold    int
	FallbackWindowSeconds     int
	FallbackTTLSeconds        int
}
