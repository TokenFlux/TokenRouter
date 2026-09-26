//go:build unit

package integration

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	creativeredis "github.com/TokenFlux/TokenRouter/internal/creative/rediscache"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/TokenFlux/TokenRouter/internal/creative"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 全链路冒烟（miniredis + fake repo/executor）：
// 创建 → 入队 → Reserve → 执行（fake provider）→ 成功结算 → 取内容 → ack → 再取失败。
// 选择 unit 层而非 integration 的原因：现有 integration 框架依赖 docker 化的 PG/Redis，
// 创作台仓储需要 ent+PG 才能实例化；本测试用原生 Redis 队列与 transient store（miniredis）
// 交叉验证原生 Public/Results/worker 全链路；PG 事务由独立 integration 测试验证。
// ---------------------------------------------------------------------------

// smokeFakeRunRepo 是 CreativeRunRepository 的内存实现（仅实现本链路用到的方法）。
type smokeFakeRunRepo struct {
	runs    map[string]*creative.CreativeRun
	outputs map[string][]*creative.CreativeRunOutput
}

func newSmokeFakeRunRepo() *smokeFakeRunRepo {
	return &smokeFakeRunRepo{
		runs:    make(map[string]*creative.CreativeRun),
		outputs: make(map[string][]*creative.CreativeRunOutput),
	}
}

func (r *smokeFakeRunRepo) CreateCreativeRun(ctx context.Context, params creative.CreateCreativeRunParams) (*creative.CreativeRun, error) {
	workspaceID := params.WorkspaceID
	run := &creative.CreativeRun{
		RunID:                params.RunID,
		UserID:               params.UserID,
		WorkspaceID:          &workspaceID,
		GroupID:              params.GroupID,
		APIKeyID:             params.APIKeyID,
		Model:                params.Model,
		Operation:            params.Operation,
		RequestedOutputCount: params.RequestedOutputCount,
		ImageSize:            params.ImageSize,
		Status:               creative.CreativeRunStatusQueued,
		EstimatedCost:        params.EstimatedCost,
		HoldAmount:           &params.HoldAmount,
		BaseUnitPrice:        params.BaseUnitPrice,
	}
	r.runs[run.RunID] = run
	outputs := make([]*creative.CreativeRunOutput, 0, params.RequestedOutputCount)
	for index := 0; index < params.RequestedOutputCount; index++ {
		outputs = append(outputs, &creative.CreativeRunOutput{RunID: run.RunID, OutputIndex: index, Status: creative.CreativeRunOutputStatusPending})
	}
	r.outputs[run.RunID] = outputs
	return run, nil
}

func (r *smokeFakeRunRepo) GetCreativeRunByRunID(ctx context.Context, runID string) (*creative.CreativeRun, error) {
	run, ok := r.runs[runID]
	if !ok {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

func (r *smokeFakeRunRepo) GetCreativeRunByRunIDForOwner(ctx context.Context, scope creative.CreativeRunScope, runID string) (*creative.CreativeRun, error) {
	run, err := r.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.UserID != scope.UserID || run.WorkspaceID == nil || *run.WorkspaceID != scope.WorkspaceID {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

func (r *smokeFakeRunRepo) GetCreativeRunByIdempotencyKey(ctx context.Context, scope creative.CreativeRunScope, key string) (*creative.CreativeRun, error) {
	return nil, creative.ErrCreativeRunNotFound
}

func (r *smokeFakeRunRepo) ListCreativeRunsForOwner(ctx context.Context, scope creative.CreativeRunScope, filter creative.CreativeRunFilter) ([]*creative.CreativeRun, error) {
	return nil, nil
}

func (r *smokeFakeRunRepo) TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts creative.CreativeRunTransitionOptions) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if !creative.CanTransitionCreativeRun(run.Status, toStatus) {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = toStatus
	if opts.ReleaseTargetStatus != "" {
		run.ReleaseTargetStatus = opts.ReleaseTargetStatus
	}
	return nil
}

func (r *smokeFakeRunRepo) MarkCreativeRunRunning(ctx context.Context, runID string, accountID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if run.Status == creative.CreativeRunStatusRunning {
		return nil
	}
	if !creative.CanTransitionCreativeRun(run.Status, creative.CreativeRunStatusRunning) {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = creative.CreativeRunStatusRunning
	if accountID > 0 {
		run.AccountID = &accountID
	}
	run.StartedAt = &now
	return nil
}

func (r *smokeFakeRunRepo) SetCreativeRunAccountID(ctx context.Context, runID string, accountID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if accountID > 0 {
		run.AccountID = &accountID
	}
	return nil
}

func (r *smokeFakeRunRepo) MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if run.Status != creative.CreativeRunStatusRunning && run.Status != creative.CreativeRunStatusProviderSucceeded && run.Status != creative.CreativeRunStatusSettlementPending {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = creative.CreativeRunStatusSucceeded
	run.ActualCost = &actualCost
	run.CompletedAt = &now
	return nil
}

func (r *smokeFakeRunRepo) UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error {
	for _, output := range r.outputs[runID] {
		if output.OutputIndex == outputIndex {
			if output.Status == creative.CreativeRunOutputStatusAcked {
				return nil
			}
			output.Status = status
			output.MimeType = &mimeType
			output.ByteSize = &byteSize
			output.TransientExpiresAt = transientExpiresAt
			output.ErrorCode = &errorCode
			output.ErrorMessage = &errorMessage
			return nil
		}
	}
	return creative.ErrCreativeOutputNotFound
}

func (r *smokeFakeRunRepo) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*creative.CreativeRunOutput, error) {
	for _, output := range r.outputs[runID] {
		if output.OutputIndex == outputIndex {
			return output, nil
		}
	}
	return nil, creative.ErrCreativeOutputNotFound
}

func (r *smokeFakeRunRepo) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*creative.CreativeRunOutput, error) {
	return r.outputs[runID], nil
}

func (r *smokeFakeRunRepo) MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error {
	output, err := r.GetCreativeRunOutput(ctx, runID, outputIndex)
	if err != nil {
		return err
	}
	if output.Status == creative.CreativeRunOutputStatusAcked {
		return nil
	}
	if output.Status != creative.CreativeRunOutputStatusSucceeded {
		return creative.ErrCreativeOutputNotReady
	}
	output.Status = creative.CreativeRunOutputStatusAcked
	output.AckedAt = &now
	return nil
}

func (r *smokeFakeRunRepo) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*creative.CreativeRun, error) {
	return nil, nil
}

func (r *smokeFakeRunRepo) IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.AttemptCount++
	return run.AttemptCount, nil
}

func (r *smokeFakeRunRepo) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.SettlementAttemptCount++
	return run.SettlementAttemptCount, nil
}

func (r *smokeFakeRunRepo) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.ReleaseAttemptCount++
	return run.ReleaseAttemptCount, nil
}

func (r *smokeFakeRunRepo) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	run.ProvisioningPhase = phase
	return nil
}

func (r *smokeFakeRunRepo) MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, accountID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if accountID > 0 {
		run.AccountID = &accountID
	}
	run.ProviderResultRecordedAt = &now
	if run.Status == creative.CreativeRunStatusRunning {
		run.Status = creative.CreativeRunStatusProviderSucceeded
	}
	return nil
}

func (r *smokeFakeRunRepo) SetCreativeRunReconcileError(ctx context.Context, runID, message string, next time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if message == "" {
		run.LastReconcileError = nil
	} else {
		run.LastReconcileError = &message
	}
	if next.IsZero() {
		run.NextReconcileAt = nil
	} else {
		run.NextReconcileAt = &next
	}
	return nil
}

// smokeFakeBillingRepo 记录 capture/release 调用。
type smokeFakeBillingRepo struct {
	captureN int
	releaseN int
}

func (r *smokeFakeBillingRepo) Reserve(ctx context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	return &billingcore.TaskFundsResult{Applied: true, HoldAmountUSD: cmd.HoldAmount, EstimatedAmountUSD: cmd.HoldAmount, BalanceAmountUSD: cmd.HoldAmount}, nil
}

func (r *smokeFakeBillingRepo) Capture(ctx context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	r.captureN++
	return &billingcore.TaskFundsResult{Applied: true, ActualAmountUSD: cmd.ActualBaseAmountUSD}, nil
}

func (r *smokeFakeBillingRepo) Release(ctx context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	r.releaseN++
	return &billingcore.TaskFundsResult{Applied: true}, nil
}

// smokeFakeExecutor 返回固定输出。
type smokeFakeExecutor struct{}

func (e *smokeFakeExecutor) Prepare(ctx context.Context, run creative.CreativeRun) (*creative.CreativeExecution, error) {
	return &creative.CreativeExecution{
		AccountID:     55,
		Target:        e,
		UpstreamModel: run.Model,
		ReleaseFunc:   func() {},
	}, nil
}

func (e *smokeFakeExecutor) Execute(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error) {
	return &creative.CreativeExecuteResult{
		Outputs:   []creative.CreativeOutput{{Index: 0, Bytes: []byte("smoke-image-bytes"), Mime: "image/png"}},
		AccountID: 55,
	}, nil
}

func (e *smokeFakeExecutor) IsRetryable(err error) bool { return false }

// smokeFakeManagedKeyRepo 供应固定隐藏 Key。
type smokeFakeManagedKeyRepo struct{ key *apikey.APIKey }

func (r *smokeFakeManagedKeyRepo) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*apikey.APIKey, error) {
	if r.key != nil {
		return r.key, nil
	}
	return nil, apikey.ErrAPIKeyNotFound
}

func (r *smokeFakeManagedKeyRepo) CreateManagedKey(ctx context.Context, key *apikey.APIKey) error {
	key.ID = 900
	r.key = key
	return nil
}

type smokeFakeUserRepo struct{}

func (r *smokeFakeUserRepo) GetByID(ctx context.Context, id int64) (creative.UserAccess, error) {
	return &identity.User{ID: id}, nil
}

type smokeFakeGroupRepo struct {
	price1k float64
}

func (r *smokeFakeGroupRepo) GetByIDLite(ctx context.Context, id int64) (*creative.GroupView, error) {
	price := r.price1k
	return &creative.GroupView{ID: id, Name: "Smoke Group", Platform: capability.PlatformGemini, Active: true, AllowImageGeneration: true, RateMultiplier: 1, Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}, Price: billingcore.PriceGroup{ModelPricing: []routing.ModelPricingEntry{{Models: []string{"*"}, BillingMode: routing.BillingModeImage, PerRequestPrice: &price}}}}, nil
}

func (r *smokeFakeGroupRepo) ListActive(context.Context) ([]creative.GroupView, error) {
	return nil, nil
}

type smokeFakeAccountRepo struct{}

func (r *smokeFakeAccountRepo) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]creative.CatalogAccount, error) {
	return []creative.CatalogAccount{creativeprovider.CatalogAccount(&account.Record{ID: 55, Status: billingcore.StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"gemini-3.1-flash-image": "gemini-3.1-flash-image"}}})}, nil
}

type smokeFakeRateRepo struct{}

func (r *smokeFakeRateRepo) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	return nil, nil
}

type smokeCreativeSettingReader struct{}

func (smokeCreativeSettingReader) IsCreativeEnabled(context.Context) bool { return true }

func (smokeCreativeSettingReader) GetCreativeModelSettings(context.Context) []creative.CreativeModelSetting {
	return []creative.CreativeModelSetting{{
		GroupID:    12,
		Model:      "gemini-3.1-flash-image",
		Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit},
	}}
}

// TestCreativeFullChainSmoke 串起创作台全链路（原生 Redis 实现 + miniredis）。
func TestCreativeFullChainSmoke(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	cfg := &config.Config{
		Creative: config.CreativeConfig{
			Enabled:                 true,
			QueueEnabled:            true,
			TransientTTLSeconds:     1800,
			MaxAssetBytes:           33554432,
			MaxTotalInputBytes:      67108864,
			MaxPromptChars:          8000,
			DefaultResponseMimeType: "image/png",
			DefaultImageSize:        "1K",
			ExecuteTimeoutSeconds:   300,
			MaxExecuteAttempts:      3,
			JobLockTTLSeconds:       300,
			StaleActiveAfterSeconds: 600,
			DelayedMoveLimit:        100,
			RecoverLimit:            100,
		},
		Default: config.DefaultConfig{APIKeyPrefix: "sk-"},
	}

	repo := newSmokeFakeRunRepo()
	billing := &smokeFakeBillingRepo{}
	queue := creativeredis.NewCreativeQueue(client, &creativeredis.QueueOptions{
		InflightKeyPrefix:  cfg.Creative.InflightKeyPrefix,
		InflightTTLSeconds: cfg.Creative.InflightTTLSeconds,
		JobLockTTLSeconds:  cfg.Creative.JobLockTTLSeconds,
		LockKeyPrefix:      cfg.Creative.LockKeyPrefix,
		QueueActiveKey:     cfg.Creative.QueueActiveKey,
		QueueDelayedKey:    cfg.Creative.QueueDelayedKey,
		QueueReadyKey:      cfg.Creative.QueueReadyKey,
	})
	store := creativeredis.NewCreativeTransientStore(client, &creativeredis.TransientOptions{TransientTTLSeconds: cfg.Creative.TransientTTLSeconds})
	results := &creative.Results{Repo: repo, TransientStore: store, Queue: queue, Funding: creative.Funding{Store: billing}, TransientTTL: time.Duration(cfg.Creative.TransientTTLSeconds) * time.Second}
	managed := apikey.ManagedKeys{Store: &smokeFakeManagedKeyRepo{}, Prefix: cfg.Default.APIKeyPrefix, ManagedBy: creative.CreativeManagedBy, NamePrefix: "creative-studio"}
	prices := billingcore.NewPriceResolver(nil, billingtestkit.Calculator(0, nil, nil), modelidentity.Identity, nil)
	svc := &creative.Public{
		Repo: repo, UserRepo: &smokeFakeUserRepo{}, AccountRepo: &smokeFakeAccountRepo{}, GroupRepo: &smokeFakeGroupRepo{price1k: 0.02}, UserGroupRateRepo: &smokeFakeRateRepo{}, Queue: queue, TransientStore: store, Results: results, Settings: smokeCreativeSettingReader{}, UserNotFound: identity.ErrUserNotFound,
		Options: creative.PublicOptions{Enabled: cfg.Creative.Enabled, MaxAssetBytes: cfg.Creative.MaxAssetBytes, MaxTotalInputBytes: cfg.Creative.MaxTotalInputBytes, MaxPromptChars: cfg.Creative.MaxPromptChars, DefaultImageSize: cfg.Creative.DefaultImageSize},
		EnsureKey: func(ctx context.Context, u, g int64) (int64, error) {
			key, err := managed.Ensure(ctx, u, g)
			if err != nil {
				return 0, err
			}
			return key.ID, nil
		},
		ImageUnitPrice: func(ctx context.Context, g *creative.GroupView, m, size string) (float64, bool) {
			price, err := prices.ResolveImageUnitPrice(ctx, billingcore.PricingInput{Model: m, GroupID: &g.ID, Group: &g.Price}, size)
			return price, err == nil
		},
		SubscriptionMultiplier: func(context.Context, int64, *creative.GroupView, float64) (float64, bool) { return 0, false },
	}
	worker := creative.NewCreativeRunWorker(queue, repo, store, &smokeFakeExecutor{}, results, creative.CreativeWorkerOptions{ReserveBlockTimeout: time.Second, JobLockTTL: time.Minute, LockConflictDelay: time.Millisecond, ErrorRetryDelay: time.Millisecond, ErrorBackoff: time.Millisecond, StaleActiveAfter: time.Minute, MaxAttempts: 3}, creative.WorkerPorts{})
	ctx := context.Background()

	// 1. 创建任务（含源图 + prompt，经校验/审核跳过/估价/预占/暂存/入队）。
	png := smokeTestPNG(t)
	scope := creative.CreativeRunScope{UserID: 7, WorkspaceID: "11111111-1111-4111-8111-111111111111"}
	created, err := svc.CreateRun(ctx, scope, creative.CreateCreativeRunParamsPublic{
		GroupID:      12,
		Model:        "gemini-3.1-flash-image",
		Operation:    creative.CreativeOperationGenerate,
		Prompt:       "smoke prompt",
		SourceImages: []creative.CreativeInputImage{{Bytes: png, Mime: "image/png"}},
		ImageSize:    "1K",
	}, "smoke-idem-key")
	require.NoError(t, err)
	require.Equal(t, creative.CreativeRunStatusQueued, created.Status)
	runID := created.ID

	// 2. worker 从原生队列实现 Reserve → 执行 fake provider → 成功结算。
	require.NoError(t, worker.RunOnce(ctx))
	require.Equal(t, creative.CreativeRunStatusSucceeded, repo.runs[runID].Status)
	require.Equal(t, 1, billing.captureN)
	require.Equal(t, creative.CreativeRunOutputStatusSucceeded, repo.outputs[runID][0].Status)

	// 3. 取回输出字节。
	content, err := svc.GetOutputContent(ctx, scope, runID, 0)
	require.NoError(t, err)
	require.Equal(t, []byte("smoke-image-bytes"), content.Content)
	require.Equal(t, "image/png", content.ContentType)

	// 4. ack 删除临时输出。
	require.NoError(t, svc.AckOutput(ctx, scope, runID, 0))
	require.Equal(t, creative.CreativeRunOutputStatusAcked, repo.outputs[runID][0].Status)
	_, err = store.LoadOutput(ctx, runID, 0)
	require.Error(t, err, "ack 后临时输出必须已删除")

	// 5. 再次获取返回 410 语义错误。
	_, err = svc.GetOutputContent(ctx, scope, runID, 0)
	require.ErrorIs(t, err, creative.ErrCreativeOutputExpired)
}

// smokeTestPNG 生成一张最小合法 PNG。
func smokeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// 新成功事实端口沿用本冒烟测试的内存仓储；真正的原子回滚由 PostgreSQL 回归覆盖。
func (r *smokeFakeRunRepo) RecordProviderOutcome(ctx context.Context, id string, accountID int64, outputs []creative.CreativeRunOutput, now time.Time) error {
	run, err := r.GetCreativeRunByRunID(ctx, id)
	if err != nil {
		return err
	}
	if run.ProviderResultRecordedAt != nil {
		return nil
	}
	snapshots := make([]*creative.CreativeRunOutput, len(outputs))
	for i := range outputs {
		value := outputs[i]
		snapshots[i] = &value
	}
	r.outputs[id] = snapshots
	return r.MarkCreativeRunProviderSucceeded(ctx, id, accountID, now)
}

func (r *smokeFakeRunRepo) CompleteProviderOutcome(ctx context.Context, id string, cost float64, lost bool, now time.Time) error {
	if err := r.MarkCreativeRunSucceeded(ctx, id, cost, now); err != nil {
		return err
	}
	if lost {
		r.runs[id].Status = creative.CreativeRunStatusResultLost
	}
	return nil
}
