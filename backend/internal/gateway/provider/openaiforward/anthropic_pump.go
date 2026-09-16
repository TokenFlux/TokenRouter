// 原生 Anthropic 转换的逐行读间隔与关闭责任保持原有边界。
package openaiforward

import (
	"bufio"
	"errors"
	"io"
	"time"
)

// ErrAnthropicStreamIdle 表示上游流读间隔超时（见上方文件注释）。
var ErrAnthropicStreamIdle = errors.New("stream data interval timeout")

// anthropicNativeLineEvent 是行泵交付的单次读取结果：line 为一行 SSE 文本，
// err 为 scanner 读错误（流自然结束时 next 返回 io.EOF，不经过本字段）。
type anthropicNativeLineEvent struct {
	line string
	err  error
}

// AnthropicLinePump 以独立 goroutine 泵送 scanner 的行，并对逐行到达
// 间隔施加 interval 上限（<=0 表示禁用，保持无界读的旧行为）。
type AnthropicLinePump struct {
	events   chan anthropicNativeLineEvent
	done     chan struct{}
	timer    *time.Timer
	interval time.Duration
}

// NewAnthropicLinePump 启动泵 goroutine；调用方 defer pump.stop()。
func NewAnthropicLinePump(scanner *bufio.Scanner, interval time.Duration) *AnthropicLinePump {
	p := &AnthropicLinePump{
		events:   make(chan anthropicNativeLineEvent, 16),
		done:     make(chan struct{}),
		interval: interval,
	}
	if interval > 0 {
		p.timer = time.NewTimer(interval)
	}
	go func() {
		defer close(p.events)
		for scanner.Scan() {
			select {
			case p.events <- anthropicNativeLineEvent{line: scanner.Text()}:
			case <-p.done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case p.events <- anthropicNativeLineEvent{err: err}:
			case <-p.done:
			}
		}
	}()
	return p
}

// next 阻塞返回下一行。返回 io.EOF 表示上游正常收流；ErrAnthropicStreamIdle
// 表示 interval 内无任何数据到达（计时从收到上一行时起算，事件处理耗时不算入，
// 与 readOpenAICompatBufferedTerminal 的 resetTimeout 语义一致）。
func (p *AnthropicLinePump) Next() (string, error) {
	var timeoutCh <-chan time.Time
	if p.timer != nil {
		timeoutCh = p.timer.C
	}
	select {
	case ev, ok := <-p.events:
		if !ok {
			return "", io.EOF
		}
		p.resetTimer()
		return ev.line, ev.err
	case <-timeoutCh:
		return "", ErrAnthropicStreamIdle
	}
}

// resetTimer 在收到一行后重启间隔计时器。
func (p *AnthropicLinePump) resetTimer() {
	if p.timer == nil {
		return
	}
	if !p.timer.Stop() {
		select {
		case <-p.timer.C:
		default:
		}
	}
	p.timer.Reset(p.interval)
}

// stop 终止泵 goroutine。注意：goroutine 若正阻塞在 scanner.Read 上，需由
// 调用方关闭 resp.Body（间隔超时分支已做）才能真正退出。
func (p *AnthropicLinePump) Stop() {
	close(p.done)
	if p.timer != nil {
		if !p.timer.Stop() {
			select {
			case <-p.timer.C:
			default:
			}
		}
	}
}
