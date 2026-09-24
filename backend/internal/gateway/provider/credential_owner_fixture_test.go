//go:build unit

package provider_test

import (
	"context"
	"errors"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// credentialReadStore 只提供这些凭据契约读取的账号，其余未用端口不构造模拟服务。
type credentialReadStore struct {
	gatewayprovider.ExecutionAccountStore
	accountsByID map[int64]*gatewayprovider.ExecutionAccount
}

func (s *credentialReadStore) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	if value, ok := s.accountsByID[id]; ok {
		return value, nil
	}
	return nil, errors.New("account not found")
}
func grokCredentialMutationSnapshot(value *gatewayprovider.ExecutionAccount) accountcore.CredentialMutationSnapshot {
	return accountcore.GrokCredentialMutationSnapshot(gatewayprovider.ExecutionRecord(value))
}
func credentialMutationForTest(value forwardcore.GrokCredentialFailure) accountcore.GrokCredentialMutation {
	return accountcore.GrokCredentialMutation{Permanent: value.Permanent, Transient: value.Transient, Reason: string(value.Reason), Snapshot: value.Snapshot(), VerifyMissing: value.Reason == forwardcore.GrokCredentialReasonMissing, VerifyProxy: value.Reason == forwardcore.GrokCredentialReasonProxyInvalid}
}
func credentialBlocked(value *gatewayhttp.RequestCredentialExecutor, target *gatewayprovider.ExecutionAccount) bool {
	return value.Runtime.Runtime.Blocked(target.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(gatewayprovider.ExecutionRecord(target)) })
}
func newRequestCredentialsFixture(store gatewayprovider.ExecutionAccountStore, tokens *accountcore.GrokTokenSource) *gatewayhttp.RequestCredentialExecutor {
	blocks := accountcore.NewRuntimeBlockState(time.Now)
	recovery := &accountcore.GrokCredentialRecovery{Runtime: blocks, Warn: slog.Warn}
	source := &accountcore.OpenAIExecutionCredentials{}
	if store != nil {
		source.Parent = func(ctx context.Context, id int64) (*accountcore.Record, error) {
			value, err := store.GetByID(ctx, id)
			return gatewayprovider.ExecutionRecord(value), err
		}
		recovery.Read = func(ctx context.Context, id int64) (*accountcore.Record, error) {
			if held, ok := ctx.Value(credentialMutationHoldKey{}).(*credentialMutationHold); ok {
				return held.read(ctx)
			}
			return source.Parent(ctx, id)
		}
		recovery.State, _ = store.(accountcore.GrokCredentialStateWriter)
	}
	if tokens != nil {
		source.Grok = tokens.GetAccessToken
		recovery.Invalidate = tokens.InvalidateToken
	}
	return &gatewayhttp.RequestCredentialExecutor{Runtime: &gatewayprovider.RequestCredentials{Source: source, HasGrokTokenSource: tokens != nil, Recovery: recovery, Runtime: blocks}}
}

// 占锁夹具通过真实 Apply 停在读取阶段，测试无需开放恢复器的私有锁 API。
// 取消占锁操作后等待它退出，不执行任何条件写入。
type credentialMutationHoldKey struct{}
type credentialMutationHold struct {
	core    *accountcore.GrokCredentialRecovery
	id      int64
	started chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc
	restore func()
}

func newCredentialMutationHold(core *accountcore.GrokCredentialRecovery, id int64) *credentialMutationHold {
	return &credentialMutationHold{core: core, id: id, started: make(chan struct{}), done: make(chan struct{})}
}
func (h *credentialMutationHold) read(ctx context.Context) (*accountcore.Record, error) {
	close(h.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (h *credentialMutationHold) Lock(ctx context.Context) error {
	if h.core.Read == nil {
		h.core.Read = func(ctx context.Context, _ int64) (*accountcore.Record, error) { return h.read(ctx) }
		h.restore = func() { h.core.Read = nil }
	}
	run, cancel := context.WithCancel(context.WithValue(ctx, credentialMutationHoldKey{}, h))
	h.cancel = cancel
	go func() {
		defer close(h.done)
		_, _ = h.core.Apply(run, &accountcore.Record{ID: h.id}, accountcore.GrokCredentialMutation{Transient: true})
	}()
	select {
	case <-h.started:
		return nil
	case <-ctx.Done():
		cancel()
		<-h.done
		return ctx.Err()
	}
}
func (h *credentialMutationHold) Unlock() {
	h.cancel()
	<-h.done
	if h.restore != nil {
		h.restore()
	}
}
