// Realtime 连接由原生会话持有；关闭同步释放连接与应用活动登记，保留原取消策略。
package grok

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type RealtimeDialOptions struct {
	BaseURL, Model           string
	Token                    string `json:"-"`
	CLIHeaders, ApplyHeaders func(http.Header)
	Dial                     func(context.Context, string, http.Header) (upstream.FrameConn, int, error)
	Enter                    func() (func(), error)
}
type RealtimeDialError struct {
	StatusCode int
	Err        error
}

func (e *RealtimeDialError) Error() string { return e.Err.Error() }
func (e *RealtimeDialError) Unwrap() error { return e.Err }

type RealtimeSession struct {
	upstream.FrameConn
	done     func()
	once     sync.Once
	closeErr error
}

func (s *RealtimeSession) Ready() bool { return s != nil && s.FrameConn != nil }
func (s *RealtimeSession) Close() error {
	if !s.Ready() {
		return nil
	}
	s.once.Do(func() {
		s.closeErr = s.FrameConn.Close()
		if s.done != nil {
			s.done()
		}
	})
	return s.closeErr
}

// DialRealtime 仅交换 URL/Header 和技术连接，账号权限及价格仍在调用方。
func DialRealtime(ctx context.Context, options RealtimeDialOptions) (*RealtimeSession, error) {
	u, err := url.Parse(options.BaseURL)
	if err != nil {
		return nil, err
	}
	u.Scheme = "wss"
	query := u.Query()
	query.Set("model", firstNonEmpty(options.Model, "grok-voice-latest"))
	u.RawQuery = query.Encode()
	headers := http.Header{"Authorization": []string{"Bearer " + options.Token}}
	if options.CLIHeaders != nil && (MediaCodec{}).IsGrokCLIProxyTarget(u.String()) {
		options.CLIHeaders(headers)
	}
	if options.ApplyHeaders != nil {
		options.ApplyHeaders(headers)
	}
	var done func()
	if options.Enter != nil {
		done, err = options.Enter()
		if err != nil {
			return nil, err
		}
	}
	conn, status, err := options.Dial(ctx, u.String(), headers)
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		if done != nil {
			done()
		}
		return nil, &RealtimeDialError{StatusCode: status, Err: err}
	}
	return &RealtimeSession{FrameConn: conn, done: done}, nil
}

// ProbeRealtime 保留探测仅握手即关闭及原始错误形状，不持有连接租约。
func ProbeRealtime(ctx context.Context, options RealtimeDialOptions) error {
	conn, err := DialRealtime(ctx, options)
	if err != nil {
		var failure *RealtimeDialError
		if errors.As(err, &failure) {
			return failure.Err
		}
		return err
	}
	return conn.Close()
}
