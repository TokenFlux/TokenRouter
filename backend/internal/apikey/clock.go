package apikey

import "time"

// now 保持每个原取时点独立读取；app 构造后不再变更时钟函数。
func (s *APIKeyService) now() time.Time {
	if s != nil && s.cfg != nil && s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}
func (w *AuthCacheInvalidationWorker) now() time.Time {
	if w.local != nil {
		return w.local.now()
	}
	return time.Now()
}
