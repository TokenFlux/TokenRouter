package server

// Options 只包含监听与 HTTP 协议参数；配置优先级由 app 完成投影。
type Options struct {
	Address                  string
	Mode                     string
	TrustedProxies           []string
	TrustedProxiesConfigured bool
	ReadHeaderTimeout        int
	IdleTimeout              int
	MaxHeaderBytes           int
	MaxRequestBodySize       int64
	H2C                      H2COptions
}

// H2COptions 保留 HTTP/2 的原连接和缓冲上限。
type H2COptions struct {
	Enabled                      bool
	MaxConcurrentStreams         uint32
	IdleTimeout                  int
	MaxReadFrameSize             int
	MaxUploadBufferPerConnection int
	MaxUploadBufferPerStream     int
}
