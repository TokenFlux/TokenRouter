// SSE 多行 data 拼接复用现有宽容边界，不改变流事件的发出时机。
package openai

import (
	"strings"

	"github.com/tidwall/gjson"
)

type SSEDataAccumulator struct {
	lines []string
}

func (a *SSEDataAccumulator) AddLine(line string, fn func([]byte)) {
	if fn == nil {
		return
	}
	trimmedLine := strings.TrimRight(line, "\r\n")
	if data, ok := ExtractSSEDataLine(trimmedLine); ok {
		a.lines = append(a.lines, data)
		return
	}
	if strings.TrimSpace(trimmedLine) == "" {
		a.Flush(fn)
	}
}
func (a *SSEDataAccumulator) Flush(fn func([]byte)) {
	if fn == nil || len(a.lines) == 0 {
		return
	}
	EmitSSEDataPayloads(a.lines, fn)
	a.lines = a.lines[:0]
}
func ForEachSSEDataPayload(body string, fn func([]byte)) {
	if fn == nil || strings.TrimSpace(body) == "" {
		return
	}
	var acc SSEDataAccumulator
	for _, line := range strings.Split(body, "\n") {
		acc.AddLine(line, fn)
	}
	acc.Flush(fn)
}
func EmitSSEDataPayloads(lines []string, fn func([]byte)) {
	if fn == nil || len(lines) == 0 {
		return
	}
	if len(lines) == 1 {
		EmitSSEDataPayload(lines[0], fn)
		return
	}
	joined := strings.Join(lines, "\n")
	if gjson.Valid(joined) {
		EmitSSEDataPayload(joined, fn)
		return
	}
	for _, line := range lines {
		EmitSSEDataPayload(line, fn)
	}
}
func EmitSSEDataPayload(data string, fn func([]byte)) {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	fn([]byte(data))
}
