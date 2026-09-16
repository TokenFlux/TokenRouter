// 旧 Wire 构造只返回同一原生 Code Assist 客户端。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/service"
	codeassist "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

func NewGeminiCliCodeAssistClient() service.GeminiCliCodeAssistClient { return codeassist.NewClient() }
