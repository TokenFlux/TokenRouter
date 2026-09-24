package requeststate

import "sync"

// ResponseFailureEffects 记录已经执行的账号副作用，只能由后续输出消费一次。
type ResponseFailureEffects struct {
	mu       sync.Mutex
	status   int
	disabled bool
	present  bool
}

func (s *ResponseFailureEffects) Store(status int, disabled bool) {
	s.mu.Lock()
	s.status, s.disabled, s.present = status, disabled, true
	s.mu.Unlock()
}
func (s *ResponseFailureEffects) Consume() (int, bool, bool) {
	if s == nil {
		return 0, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status, disabled, present := s.status, s.disabled, s.present
	s.present = false
	return status, disabled, present
}
