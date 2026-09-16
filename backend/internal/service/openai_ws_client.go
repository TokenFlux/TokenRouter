// 旧 WS 技术入口只别名/委托唯一原生实现；入站编排仍使用自己的生命周期。
package service

import native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

const openAIWSMessageReadLimitBytes = native.WSMessageReadLimitBytes

type OpenAIWSTransportMetricsSnapshot = native.WSTransportMetricsSnapshot
type openAIWSClientConn = native.WSClientConn

type openAIWSClientDialer = native.WSClientDialer

func newDefaultOpenAIWSClientDialer() openAIWSClientDialer { return native.NewDefaultWSClientDialer() }

type openAIWSHandshakeError = native.WSHandshakeError
