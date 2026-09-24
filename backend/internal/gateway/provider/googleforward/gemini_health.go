package googleforward

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// observeHealth 只回写原先会同步的凭据与附加状态，不把健康快照覆盖到执行账号。
func (s *Gemini) observeHealth(ctx context.Context, target *provider.ExecutionAccount, status int, headers http.Header, body []byte) {
	record := provider.ExecutionRecord(target)
	updated := s.Errors.Observe(ctx, record, status, headers, body, provider.HealthObservationFromContext(ctx, status, headers, body, nil))
	if target != nil && updated != nil {
		target.Record.Credentials, target.Record.Extra = updated.Credentials, updated.Extra
	}
}
