// 旧入站测试继续使用原 fake；连接池行为测试已随原生实现迁移。
package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type openAIWSCountingDialer struct {
	mu             sync.Mutex
	dialCount      int
	lastTLSProfile *tlsfingerprint.Profile
}

func (d *openAIWSCountingDialer) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
	profile *tlsfingerprint.Profile,
) (openAIWSClientConn, int, http.Header, error) {
	_ = ctx
	_ = wsURL
	_ = headers
	_ = proxyURL
	d.mu.Lock()
	d.dialCount++
	d.lastTLSProfile = profile
	d.mu.Unlock()
	return &openAIWSFakeConn{}, 0, nil, nil
}

func (d *openAIWSCountingDialer) DialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dialCount
}
func (d *openAIWSCountingDialer) LastTLSProfile() *tlsfingerprint.Profile {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastTLSProfile
}

type openAIWSFakeConn struct {
	mu      sync.Mutex
	closed  bool
	payload [][]byte
}

func (c *openAIWSFakeConn) WriteJSON(ctx context.Context, value any) error {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("closed")
	}
	c.payload = append(c.payload, []byte("ok"))
	_ = value
	return nil
}
func (c *openAIWSFakeConn) ReadMessage(ctx context.Context) ([]byte, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("closed")
	}
	return []byte(`{"type":"response.completed","response":{"id":"resp_fake"}}`), nil
}
func (c *openAIWSFakeConn) Ping(ctx context.Context) error {
	_ = ctx
	return nil
}
func (c *openAIWSFakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

type openAIWSBlockingConn struct {
	readDelay time.Duration
}

func (c *openAIWSBlockingConn) WriteJSON(ctx context.Context, value any) error {
	_ = ctx
	_ = value
	return nil
}
func (c *openAIWSBlockingConn) ReadMessage(ctx context.Context) ([]byte, error) {
	delay := c.readDelay
	if delay <= 0 {
		delay = 10 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return []byte(`{"type":"response.completed","response":{"id":"resp_blocking"}}`), nil
	}
}
func (c *openAIWSBlockingConn) Ping(ctx context.Context) error {
	_ = ctx
	return nil
}
func (c *openAIWSBlockingConn) Close() error {
	return nil
}

// 应用关闭后不得通过按需入口创建第二个池，既有池也不得再次发起获取。
func TestOpenAIWSConnPoolShutdownSealsLazyCreation(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.CloseOpenAIWSPool()
	require.Nil(t, svc.getOpenAIWSConnPool())
	pool := newOpenAIWSConnPool(nil)
	pool.Close()
	_, err := pool.Acquire(context.Background(), openAIWSAcquireRequest{Account: openAIWSPoolAccountView(&Account{ID: 1}), WSURL: "wss://example.test"})
	require.ErrorIs(t, err, errOpenAIWSConnClosed)
	pool.Close()
}
