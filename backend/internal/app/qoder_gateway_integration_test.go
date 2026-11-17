//go:build integration

package app

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	keypg "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providerpg "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	routingpg "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/testutil/rediscontainer"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// Qoder HTTP service can retain user and provider leases while a disconnected
// client leaves tail usage unread. The persistent scenario delays that tail
// until disconnection is observed, then checks final usage, settlement, and
// slot release across successful, partial-error, and upstream-timeout streams.
//
// A completion worker calculates price, settles funds, and writes usage through
// production stores. Replaying that callback uses the same request identity and
// verifies one financial effect plus one usage row, while supplier call count
// stays one. Separate waiting cases reserve the user slot first and force
// cancellation, heartbeat failure, or funding denial before provider selection
// can begin.

// balanceReader adapts a SQL user balance for persistent billing admission.
type balanceReader struct {
	db *sql.DB // fixture balance queries
}

// storageQoderRuntime supplies request callbacks for the persistent Qoder HTTP
// scenario.
type storageQoderRuntime struct {
	// prepare, check, and selectProvider are assigned before the HTTP request
	// begins. The native executor invokes them for this scenario while production leases, upstream execution, and stores retain their normal work.
	// route preparation callback
	prepare func(context.Context, gateway.Request) (gateway.Request, error)
	// funding validation callback
	check func(context.Context) error
	// selected provider callback
	selectProvider func(context.Context, gateway.Request, map[int64]struct{}) (*gateway.Selection, error)
}

// GetByID returns the requested identity with its SQL balance read result. Even
// a query error returns the allocated summary, allowing billing to handle the
// error before consuming its balance.
func (r balanceReader) GetByID(ctx context.Context, id int64) (*billing.UserSummary, error) {
	u := &billing.UserSummary{ID: id}
	err := r.db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", id).Scan(&u.Balance)
	return u, err
}

// TestQoderHTTPStorageChain runs native Qoder HTTP with PostgreSQL, Redis
// leases, and a completion worker. It checks waiting rejections, tail usage
// after disconnect, partial/timeout output, replayed settlement amounts and one
// usage row, then final user/provider slot release.
func TestQoderHTTPStorageChain(t *testing.T) {
	f := newDatabaseFixture(t)
	ctx := context.Background()
	container, err := rediscontainer.Run(ctx, "redis:8.4-alpine")
	// Register any created container before startup errors can stop this test.
	// Later client cleanups run first because testing reverses registration order.
	if container != nil {
		t.Cleanup(func() {
			// Setup or test cancellation must not cancel Docker removal. Start the
			// bounded cleanup context when this callback runs.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := container.Terminate(cleanupCtx); err != nil {
				t.Errorf("terminate Redis container: %v", err)
			}
		})
	}
	require.NoError(t, err)
	addr, err := container.Endpoint(ctx, "")
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	concurCache := schedulerredis.NewConcurrencyCache(rdb, 5, 60)
	concur := scheduler.NewConcurrencyService(concurCache, scheduler.Diagnostics{})
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		require.NoError(t, concur.StopContext(stopCtx))
	})

	// Redis tracks waiting requests. Cancellation, heartbeat write failure, or failed funding rechecks stop execution before provider selection.
	for _, kind := range []string{"wait-cancel", "wait-heartbeat-failure", "wait-billing-recheck"} {
		t.Run(kind, func(t *testing.T) {
			const id int64 = 990099
			// Occupy the user slot before orchestration so waiting begins before
			// provider selection; the wait observer then injects each distinct admission failure or release.
			held, e := concur.AcquireUserSlot(ctx, id, 1)
			require.NoError(t, e)
			require.True(t, held.Acquired)
			defer held.ReleaseFunc()
			requestCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			expected := errors.New("fixture waiting rejected")
			checks := 0
			ports := gateway.RequestPorts{Concurrency: concur, Check: func(context.Context, bool) error {
				checks++
				if checks == 2 {
					return expected
				}
				return nil
			}, Select: func(context.Context, map[int64]struct{}) (*gateway.Selection, error) {
				t.Fatal("等待未通过不应选择提供商")
				return nil, nil
			}}
			ports.WaitObserver = func(string) scheduler.WaitObserver {
				return scheduler.WaitObserver{Interval: time.Millisecond, Begin: func() error {
					switch kind {
					case "wait-cancel":
						cancel()
					case "wait-heartbeat-failure":
						return expected
					case "wait-billing-recheck":
						held.ReleaseFunc()
					}
					return nil
				}}
			}
			e = gateway.NewQoderUseCase(3, time.Second).Run(requestCtx, gateway.Request{UserID: id, Concurrency: 1, Stream: true}, ports, &gateway.OutputTracker{Sink: gatewayhttp.ResponseSink{Writer: httptest.NewRecorder()}})
			if kind == "wait-cancel" {
				require.ErrorIs(t, e, context.Canceled)
			} else {
				require.ErrorIs(t, e, expected)
			}
			if kind == "wait-billing-recheck" {
				require.Equal(t, 2, checks)
			} else {
				require.Equal(t, 1, checks)
			}
			held.ReleaseFunc()
			count, e := concurCache.GetUserConcurrency(ctx, id)
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
	funds := billing.NewFunds(billingpg.NewSettlementStore(f.db, timezone.NewCalendar(time.Local), nil))
	facts := usagepg.NewUsageLogRepository(f.client, f.db, nil, timezone.NewCalendar(time.Local))
	t.Cleanup(facts.StopUsageBatchers)
	pool := completion.NewUsageRecordWorkerPoolWithOptions(completion.UsageRecordWorkerPoolOptions{Observe: gatewaytelemetry.Completion, WorkerCount: 1, QueueSize: 8, TaskTimeout: 5 * time.Second})
	pool.Start()
	t.Cleanup(pool.Stop)
	keys := keypg.NewKeyStore(f.client, f.db, nil)
	auth := apikey.NewAPIKeyService(keys, nil, nil, nil, nil, nil, &apikey.Options{})
	groups := routingpg.NewGroupStore(f.client, f.db, routingpg.GroupStoreOptions{})
	providers := providerpg.NewProviderStore(f.client, f.db, providerpg.ProviderStoreOptions{Group: func(g *dbent.Group) *accessview.GroupConfig {
		return (*accessview.GroupConfig)(routingpg.GroupFromEnt(g))
	}})
	eligibility := billing.NewEligibility(billingredis.NewBillingCache(rdb), balanceReader{f.db}, nil, func() billing.EligibilityOptions { return billing.EligibilityOptions{} }, nil)
	eligibility.Start()
	t.Cleanup(eligibility.Stop)
	price := &pricing.ResolvedPricing{Mode: pricing.BillingModeToken, Source: pricing.PricingSourceConfig, BasePricing: &pricing.ModelPricing{InputPricePerToken: 0.01, OutputPricePerToken: 0.02}}
	for _, tc := range []struct {
		name                string // persistent HTTP scenario
		stream, partial     bool   // stream: requested streaming mode; partial: injected terminal error
		timeout, disconnect bool   // timeout: upstream budget expiry; disconnect: client tail interruption
	}{{name: "nonstream"}, {name: "stream", stream: true}, {name: "partial-failure", stream: true, partial: true}, {name: "upstream-timeout", stream: true, partial: true, timeout: true}, {name: "client-disconnect-tail-usage", stream: true, disconnect: true}} {
		t.Run(tc.name, func(t *testing.T) {
			uid := uuid.NewString()
			user, err := f.client.User.Create().SetEmail(uid + "@test.test").SetPasswordHash("fixture-only").SetBalance(10).SetConcurrency(1).Save(ctx)
			require.NoError(t, err)
			group, err := f.client.Group.Create().SetName("test-" + uid).SetAllowedProtocols([]protocol.ProtocolID{protocol.ProtocolOpenAIChatCompletions}).SetProtocolFallbacks(map[protocol.ProtocolID][]protocol.ProtocolID{protocol.ProtocolOpenAIChatCompletions: {protocol.ProtocolQoderChat}}).Save(ctx)
			require.NoError(t, err)
			key, err := f.client.APIKey.Create().SetUserID(user.ID).SetGroupID(group.ID).SetKey("sk-test-" + uid).SetName("fixture").SetQuota(100).SetBillingMode("balance").Save(ctx)
			require.NoError(t, err)
			acc, err := f.client.Provider.Create().SetName("test-" + uid).SetPlatform("qoder").SetType("cosy").SetCredentials(map[string]any{"upstream_protocols": []string{"qoder_chat"}}).SetConcurrency(1).AddGroupIDs(group.ID).Save(ctx)
			require.NoError(t, err)
			var calls, completed atomic.Int32
			nativeBody := successfulQoderStream
			if tc.partial {
				nativeBody = strings.Replace(nativeBody, "data: {\"body\":\"[DONE]\"}\n\n", qoderFailureFrame, 1)
			}
			// Hold supplier tail usage until HTTP detects the client disconnect.
			// This lets the test inspect occupied leases before releasing the remaining supplier frames for completion.
			tailAllowed := make(chan struct{})
			clientGone := make(chan struct{})
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "auto", r.Header.Get("x-model-key"))
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.timeout || tc.disconnect {
					first, tail, _ := strings.Cut(successfulQoderStream, "\n\n")
					// Flush the first semantic frame before holding the remaining tail.
					// The fixture ignores this write result and uses explicit timeout/disconnect signals to drive later server behavior.
					_, _ = io.WriteString(w, first+"\n\n")
					flush, ok := w.(http.Flusher)
					require.True(t, ok)
					flush.Flush()
					if tc.timeout {
						// Send usage without the terminal frame to keep the client waiting
						// until its execution budget expires; the controlled fixture discards this server write result.
						_, _ = io.WriteString(w, strings.Replace(tail, "data: {\"body\":\"[DONE]\"}\n\n", "", 1))
						flush.Flush()
						// Omit the terminal frame and keep the server handler open until
						// execution timeout cancels upstream, exposing tail usage retained before that cancellation.
						<-r.Context().Done()
						return
					}
					select {
					case <-tailAllowed:
					case <-r.Context().Done():
						return
					}
					// The disconnect branch permits tail delivery only after the test
					// inspects occupied slots. It discards this final fixture write result before returning the server handler.
					_, _ = io.WriteString(w, tail)
					return
				}
				// Ordinary and partial-error cases send their complete controlled frames.
				// The fixture has no returned write-error channel; client output and completion assertions judge the observed result.
				_, _ = io.WriteString(w, nativeBody)
			}))
			defer provider.Close()
			profile := qoder.MustProfileForSite(qoder.SiteGlobal)
			profile.GatewayBaseURL = provider.URL
			client := qoder.NewClientForProfile(profile)
			session, err := qoder.NewSessionForSite(&qoder.AuthIdentity{UID: "fixture", SecurityOauthToken: "fixture"}, qoder.NewMachine(), qoder.SiteGlobal)
			require.NoError(t, err)
			options := qoder.ExecuteOptions{}
			if tc.timeout {
				options.Timeout = 100 * time.Millisecond
			}
			executor := qoder.NewExecutor(options)
			done := make(chan error, 2)
			var last upstream.AttemptResult
			complete := func(_ context.Context, result upstream.AttemptResult) {
				last = result
				pool.Submit(func(taskCtx context.Context) {
					cost, costErr := pricing.CalculateCost(price, pricing.CostInput{Model: "auto", Tokens: pricing.UsageTokens{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}, RateMultiplier: 1})
					if costErr != nil {
						done <- costErr
						return
					}
					command := &billing.UsageBillingCommand{RequestID: uid, APIKeyID: key.ID, UserID: user.ID, ActorUserID: user.ID, ProviderID: acc.ID, ProviderType: "cosy", GroupID: &group.ID, APIKeyBillingMode: "balance", Model: "auto", InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens, BillableAmountUSD: cost.ActualCost, BaseAmountUSD: cost.TotalCost, BalanceRateMultiplier: 1, APIKeyQuotaCost: cost.ActualCost, ProviderQuotaCost: cost.TotalCost * 2}
					// Persist the funds command before writing its usage fact. Replay
					// retains the same request identity, so the settlement store can report an already applied financial command.
					settled, settleErr := funds.Settle(taskCtx, command)
					if settleErr != nil {
						done <- settleErr
						return
					}
					if settled.Applied {
						completed.Add(1)
					}
					// Distinct provider-cost doubling exposes accidental reuse of the user charge field.
					providerCost := cost.TotalCost * 2
					_, createErr := facts.Create(taskCtx, &usage.UsageLog{UserID: user.ID, BillingUserID: user.ID, APIKeyID: key.ID, ProviderID: acc.ID, GroupID: &group.ID, RequestID: uid, Model: "auto", InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens, TotalCost: cost.TotalCost, ActualCost: cost.ActualCost, BalanceAmountUSD: cost.ActualCost, ProviderStatsCost: &providerCost, RateMultiplier: 1, Stream: tc.stream, CreatedAt: time.Now()})
					done <- createErr
				})
			}

			// The HTTP executor receives authentication and body data for each request.
			runtime := &storageQoderRuntime{}
			runtime.prepare = func(callCtx context.Context, request gateway.Request) (gateway.Request, error) {
				grp, err := groups.GetByID(callCtx, group.ID)
				if err != nil {
					return gateway.Request{}, err
				}
				request.Route = routing.Plan(routing.PlanInput{Group: grp, GroupID: &group.ID, RequestedModel: request.Model, ClientProtocol: protocol.ProtocolOpenAIChatCompletions, GroupMapping: routing.GroupMappingResult{ClientModel: request.Model, MappedModel: request.Model}})
				return request, nil
			}
			runtime.check = func(callCtx context.Context) error {
				return eligibility.Check(callCtx, billing.CheckInput{Payer: &billing.UserSummary{ID: user.ID}, Key: &billing.KeySnapshot{ID: key.ID, BillingMode: "balance"}, Group: &billing.GroupSnapshot{ID: group.ID}, Platform: "qoder"})
			}

			runtime.selectProvider = func(callCtx context.Context, request gateway.Request, excluded map[int64]struct{}) (*gateway.Selection, error) {
				record, loadErr := providers.GetByID(callCtx, acc.ID)
				if loadErr != nil {
					return nil, loadErr
				}
				snapshot := record.RoutingSnapshot()
				selectionInput := scheduler.SelectionInput{RoutePlan: request.Route, Candidates: []scheduler.SelectionCandidate{{Snapshot: &snapshot}}, ExcludedIDs: excluded}
				fresh := func(ctx context.Context, candidate scheduler.SelectionCandidate) (scheduler.SelectionCandidate, bool) {
					r, e := providers.GetByID(ctx, acc.ID)
					if e != nil {
						return candidate, false
					}
					value := r.RoutingSnapshot()
					candidate.Snapshot = &value
					cp, ok := request.Route.ResolveCandidate(value)
					candidate.Plan = &cp
					return candidate, ok
				}
				selected, ok, selectErr := scheduler.RequestLease(callCtx).Select(callCtx, selectionInput, scheduler.AttemptSelectionPorts{Acquire: func(ctx context.Context, id int64, limit int) (*scheduler.AcquireResult, bool, error) {
					r, e := concur.AcquireProviderSlot(ctx, id, limit)
					return r, true, e
				}, Fresh: fresh, CanRecheck: func() bool { return true }, Recheck: fresh, CompactAllowed: func(scheduler.SelectionCandidate) bool { return true }})
				if selectErr != nil {
					return nil, selectErr
				}
				if !ok {
					return nil, fmt.Errorf("fixture candidate unavailable")
				}
				target := &qoder.Target{ProviderID: acc.ID, Site: qoder.SiteGlobal, Metadata: qoder.RequestMetadata{APIKeyID: key.ID}, Session: func(context.Context) (*qoder.SessionContext, error) { return session, nil }, Client: func() (qoder.StreamClient, error) { return client, nil }}
				return &gateway.Selection{Snapshot: snapshot, Plan: *selected.Candidate.Plan, Acquired: true, Release: selected.Attempt.Release, Executor: executor, Input: upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIChatCompletions, Body: request.Body, Stream: request.Stream, ResponseModel: request.Model, Target: target}, Complete: complete}, nil
			}
			handler := &gatewayhttp.QoderChatHandler{Executor: gateway.NewQoderExecutor(3, time.Second, concur, runtime)}
			handler.PrepareRequest = func(c *gin.Context, p gatewayhttp.ParsedRequest) (gateway.Request, error) {
				if tc.disconnect {
					requestCtx := c.Request.Context()
					// Publish request cancellation on a dedicated signal so the test
					// can distinguish observed client departure from merely closing its local response reader.
					go func() { <-requestCtx.Done(); close(clientGone) }()
				}
				value, _ := c.Get("access")
				access, ok := value.(*apikey.AccessSnapshot)
				require.True(t, ok)
				return gateway.Request{Access: access, UserID: user.ID, Concurrency: 1, Stream: p.Stream, Body: p.Body, Model: p.Model}, nil
			}

			router := gin.New()
			router.Use(func(c *gin.Context) {
				a, ok := keyhttp.Authenticate(c, auth, keyhttp.AuthenticationOptions{})
				if !ok {
					c.Abort()
					return
				}
				c.Set("access", a)
				c.Next()
			})
			router.POST("/v1/chat/completions", handler.ChatCompletions)
			server := httptest.NewServer(router)
			defer server.Close()
			req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"auto","stream":%t,"messages":[{"role":"user","content":"hi"}]}`, tc.stream)))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+key.Key)
			response, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			if tc.disconnect {
				reader := bufio.NewReader(response.Body)
				for {
					line, readErr := reader.ReadString('\n')
					require.NoError(t, readErr)
					if strings.Contains(line, "served") {
						break
					}
				}
				require.NoError(t, response.Body.Close())
				select {
				case <-clientGone:
				case <-time.After(time.Second):
					t.Fatal("client cancellation not observed")
				}
				userSlots, readErr := concurCache.GetUserConcurrency(ctx, user.ID)
				require.NoError(t, readErr)
				require.Equal(t, 1, userSlots)
				providerSlots, readErr := concurCache.GetProviderConcurrency(ctx, acc.ID)
				require.NoError(t, readErr)
				require.Equal(t, 1, providerSlots)
				// Both leases remain occupied after disconnection. Release the supplier
				// tail now so execution collects final usage before completing and returning those slots.
				close(tailAllowed)
			} else {
				data, readErr := io.ReadAll(response.Body)
				require.NoError(t, readErr)
				require.NoError(t, response.Body.Close())
				require.Equal(t, 200, response.StatusCode, string(data))
				require.Contains(t, string(data), "served")
				if tc.partial {
					require.Contains(t, string(data), "upstream_error")
					require.Equal(t, 1, strings.Count(string(data), "data: [DONE]"))
				}
			}

			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("completion did not finish")
			}
			// Replaying completion checks persistent idempotency; the upstream request count remains one.
			complete(ctx, last)
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("completion replay did not finish")
			}
			require.EqualValues(t, 1, calls.Load())
			require.EqualValues(t, 1, completed.Load())
			var balance, quota float64
			var count int
			require.NoError(t, f.db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
			// SQL amount comparisons allow a hundred-millionth floating-point tolerance.
			require.InDelta(t, 9.82, balance, 1e-8)
			require.NoError(t, f.db.QueryRowContext(ctx, "SELECT quota_used FROM api_keys WHERE id=$1", key.ID).Scan(&quota))
			// Key quota uses the same hundred-millionth numeric comparison tolerance.
			require.InDelta(t, 0.18, quota, 1e-8)
			require.NoError(t, f.db.QueryRowContext(ctx, "SELECT count(*) FROM usage_logs WHERE request_id=$1", uid).Scan(&count))
			require.Equal(t, 1, count)
			var actual, providerCost float64
			require.NoError(t, f.db.QueryRowContext(ctx, "SELECT actual_cost,provider_stats_cost FROM usage_logs WHERE request_id=$1", uid).Scan(&actual, &providerCost))
			// Recorded cost fields retain a hundred-millionth comparison tolerance after SQL reads.
			require.InDelta(t, 0.18, actual, 1e-8)
			require.InDelta(t, 0.36, providerCost, 1e-8)
			slots, err := concurCache.GetUserConcurrency(ctx, user.ID)
			require.NoError(t, err)
			require.Zero(t, slots)
			slots, err = concurCache.GetProviderConcurrency(ctx, acc.ID)
			require.NoError(t, err)
			require.Zero(t, slots)
		})
	}
}

// Prepare delegates route preparation to the configured persistent scenario
// callback.
func (r *storageQoderRuntime) Prepare(ctx context.Context, v gateway.Request) (gateway.Request, error) {
	return r.prepare(ctx, v)
}

// Check delegates funding to the configured callback and ignores
// request/after-wait inputs.
func (r *storageQoderRuntime) Check(ctx context.Context, _ gateway.Request, _ bool) error {
	return r.check(ctx)
}

// Select delegates request and exclusions to the persistent provider-selection
// callback.
func (r *storageQoderRuntime) Select(ctx context.Context, v gateway.Request, excluded map[int64]struct{}) (*gateway.Selection, error) {
	return r.selectProvider(ctx, v, excluded)
}

// CanRefresh returns false to disable session refresh in this persistent
// fixture.
func (*storageQoderRuntime) CanRefresh(error) bool { return false }

// CanFailover returns true for fixture errors considered by the native loop.
func (*storageQoderRuntime) CanFailover(error) bool { return true }

// RefreshPending returns false because this fixture models no concurrent
// refresh.
func (*storageQoderRuntime) RefreshPending(error) bool { return false }

// QueueFailure ignores fixture waiting-counter observations while production
// cleanup remains exercised.
func (*storageQoderRuntime) QueueFailure(string, error) {}
