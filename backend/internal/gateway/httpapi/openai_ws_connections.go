package httpapi

import (
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// OpenAIWSConnections 持有唯一按需连接池与共享拨号器，关闭后不能重新建池。
type OpenAIWSConnections struct {
	Options    *openai.WSPoolOptions
	dialer     openai.WSClientDialer
	dialerOnce sync.Once
	pool       *openai.WSConnPool
	poolOnce   sync.Once
	poolMu     sync.Mutex
	closed     bool
}

// NewOpenAIWSConnections 只登记配置和拨号器，不启动连接池任务。
func NewOpenAIWSConnections(options *openai.WSPoolOptions, dialer openai.WSClientDialer) *OpenAIWSConnections {
	return &OpenAIWSConnections{Options: options, dialer: dialer}
}

func (s *OpenAIWSConnections) Pool() *openai.WSConnPool {
	if s == nil {
		return nil
	}
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	if s.closed {
		return s.pool
	}
	s.poolOnce.Do(func() {
		if s.pool == nil {
			s.pool = openai.NewWSConnPool(s.Options)
			s.pool.Start()
		}
	})
	return s.pool
}

func (s *OpenAIWSConnections) Dialer() openai.WSClientDialer {
	if s == nil {
		return nil
	}
	s.dialerOnce.Do(func() {
		if s.dialer == nil {
			s.dialer = openai.NewDefaultWSClientDialer()
		}
	})
	return s.dialer
}

func (s *OpenAIWSConnections) InvalidateAccount(accountID int64) {
	if pool := s.Pool(); pool != nil {
		pool.ClearAccount(accountID)
	}
}

// Close 保留当前池供关闭后只读观测，禁止再次按需启动。
func (s *OpenAIWSConnections) Close() {
	if s == nil {
		return
	}
	s.poolMu.Lock()
	s.closed = true
	pool := s.pool
	s.poolMu.Unlock()
	if pool != nil {
		pool.Close()
	}
}
