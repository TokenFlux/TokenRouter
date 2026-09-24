package httpapi

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai/liveattestation"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// liveFixtureInputs 仅组合Live合同所需的存储、帧连接及身份端口。
type liveFixtureInputs struct {
	transport   httpclient.UpstreamTransport
	accounts    provider.ExecutionAccountStore
	store       session.LiveCallStore
	concurrency *scheduler.ConcurrencyService
	logs        usage.UsageLogRepository
	profiles    *egressprovider.TLSProfiles
	routers     *egress.TLSFingerprintRouterService
	dialer      openai.WSClientDialer
	attestation liveattestation.Provider
	cipher      identity.SecretEncryptor
	duration    time.Duration
}

func newLiveFixture(v liveFixtureInputs) *OpenAILiveExecutor {
	aux := newAuxiliaryFixture(auxiliaryFixtureInputs{transport: v.transport, store: v.accounts, profiles: v.profiles})
	aux.Requests.Routers = v.routers
	aux.Requests.ClientPolicy.Routers = v.routers
	out := &OpenAILiveExecutor{Options: OpenAILiveOptions{MaxSessionDuration: v.duration, ObserverRetryInterval: time.Second}, Requests: aux.Requests, Store: v.store, Dialer: v.dialer, Attestation: v.attestation, AttestationCipher: v.cipher, Selection: selection.NewCompatible(selection.CompatibleDependencies{}, selection.Options{}), Routes: provider.NewRoutePlanner(nil), Background: func(_ string, fn func()) bool { go fn(); return true }}
	if v.concurrency != nil {
		out.Leases = v.concurrency.LiveLeases()
	}
	if v.logs != nil {
		out.Usage = completion.NewRecorder(completion.Dependencies{Logs: completion.SnapshotLogWriter(v.logs), Observe: telemetry.ObserveCompletion}, completion.RecorderOptions{})
	}
	return out
}

// TLS替身仅提供预热读取，实际模板及规则匹配由原生实现执行。
type liveProfileStore struct {
	egress.TLSFingerprintProfileRepository
	values []*egress.TLSFingerprintProfile
}

func (s *liveProfileStore) List(context.Context) ([]*egress.TLSFingerprintProfile, error) {
	return s.values, nil
}

type liveRouterStore struct {
	egress.TLSFingerprintRouterRepository
	values []*egress.TLSFingerprintRouter
}

func (s *liveRouterStore) List(context.Context) ([]*egress.TLSFingerprintRouter, error) {
	return s.values, nil
}
