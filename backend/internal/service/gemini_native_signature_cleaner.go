// 原入口投影原有占位签名，协议算法只保留一份。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
)

func CleanGeminiNativeThoughtSignatures(body []byte) []byte {
	return gemini.CleanNativeThoughtSignatures(body, bridge.DummyThoughtSignature)
}
