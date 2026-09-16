package service

import (
	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

func forEachOpenAISSEFrame(body string, fn func(string, []byte)) {
	s09openai.ForEachOpenAISSEFrame(body, fn)
}
