package forward

import (
	bridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// Lines 将扫描大小和实际读取留给传输 Adapter。
type Lines interface {
	Scan() bool
	Text() string
	Err() error
}

// Response 只提供本次响应的明确报文事实。
type Response struct {
	StatusCode int
	Close      func()
	Runtime    bridge.Runtime
	RequestID  string
	Headers    map[string][]string
	Lines      Lines
}

// Output 同步反馈写失败；核心只推进协议事件，不拥有 HTTP 状态或缓冲器。
type Output interface {
	CopyHeaders(map[string][]string)
	BeginJSON()
	BeginStream()
	ReverseTools([]byte) []byte
	JSONBytes([]byte)
	ResponsesJSON(*protocolopenai.ResponsesResponse)
	ChatJSON(*protocolopenai.ChatCompletionsResponse)
	Event(string, []byte) (int, error)
	Flush()
	Error(int, string, string)
	Observe(string, string, error, string, string)
}
