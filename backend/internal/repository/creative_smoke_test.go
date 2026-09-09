//go:build unit

package repository

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 全链路冒烟（miniredis + fake repo/executor）：
// 创建 → 入队 → Reserve → 执行（fake provider）→ 成功结算 → 取内容 → ack → 再取失败。
// 选择 unit 层而非 integration 的原因：现有 integration 框架依赖 docker 化的 PG/Redis，
// 创作台仓储需要 ent+PG 才能实例化；本测试用真实 Redis 队列与真实 transient store
// 交叉验证 service/worker 全链路，PG 侧由 service 层 fake repo 覆盖。
// ---------------------------------------------------------------------------

// smokeFakeRunRepo 是 CreativeRunRepository 的内存实现（仅实现本链路用到的方法）。
type smokeFakeRunRepo struct {
	runs    map[string]*service.CreativeRun
	outputs map[string][]*service.CreativeRunOutput
}

func newSmokeFakeRunRepo() *smokeFakeRunRepo {
	return &smokeFakeRunRepo{
		runs:    make(map[string]*service.CreativeRun),
		outputs: make(map[string][]*service.CreativeRunOutput),
	}
}

func (r *smokeFakeRunRepo) CreateCreativeRun(ctx context.Context, params service.CreateCreativeRunParams) (*service.CreativeRun, error) {
	workspaceID := params.WorkspaceID
	run := &service.CreativeRun{
		RunID:                params.RunID,
		UserID:               params.UserID,
		WorkspaceID:          &workspaceID,
		GroupID:              params.GroupID,
		APIKeyID:             params.APIKeyID,
		Model:                params.Model,
		Operation:            params.Operation,
		RequestedOutputCount: params.RequestedOutputCount,
		ImageSize:            params.ImageSize,
		Status:               service.CreativeRunStatusQueued,
		EstimatedCost:        params.EstimatedCost,
		HoldAmount:           &params.HoldAmount,
		BaseUnitPrice:        params.BaseUnitPrice,
	}
	r.runs[run.RunID] = run
	outputs := make([]*service.CreativeRunOutput, 0, params.RequestedOutputCount)
	for index := 0; index < params.RequestedOutputCount; index++ {
		outputs = append(outputs, &service.CreativeRunOutput{RunID: run.RunID, OutputIndex: index, Status: service.CreativeRunOutputStatusPending})
	}
	r.outputs[run.RunID] = outputs
	return run, nil
}

func (r *smokeFakeRunRepo) GetCreativeRunByRunID(ctx context.Context, runID string) (*service.CreativeRun, error) {
	run, ok := r.runs[runID]
	if !ok {
		return nil, service.ErrCreativeRunNotFound
	}
	return run, nil
}

func (r *smokeFakeRunRepo) GetCreativeRunByRunIDForOwner(ctx context.Context, scope service.CreativeRunScope, runID string) (*service.CreativeRun, error) {
	run, err := r.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.UserID != scope.UserID || run.WorkspaceID == nil || *run.WorkspaceID != scope.WorkspaceID {
		return nil, service.ErrCreativeRunNotFound
	}
	return run, nil
}

func (r *smokeFakeRunRepo) GetCreativeRunByIdempotencyKey(ctx context.Context, scope service.CreativeRunScope, key string) (*service.CreativeRun, error) {
	return nil, service.ErrCreativeRunNotFound
}

func (r *smokeFakeRunRepo) ListCreativeRunsForOwner(ctx context.Context, scope service.CreativeRunScope, filter service.CreativeRunFilter) ([]*service.CreativeRun, error) {
	return nil, nil
}

func (r *smokeFakeRunRepo) TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts service.CreativeRunTransitionOptions) error {
	run, ok := r.runs[runID]
	if !ok {
		return service.ErrCreativeRunNotFound
	}
	if !service.CanTransitionCreativeRun(run.Status, toStatus) {
		return service.ErrCreativeInvalidTransition
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
		return service.ErrCreativeRunNotFound
	}
	if run.Status == service.CreativeRunStatusRunning {
		return nil
	}
	if !service.CanTransitionCreativeRun(run.Status, service.CreativeRunStatusRunning) {
		return service.ErrCreativeInvalidTransition
	}
	run.Status = service.CreativeRunStatusRunning
	if accountID > 0 {
		run.AccountID = &accountID
	}
	run.StartedAt = &now
	return nil
}

func (r *smokeFakeRunRepo) SetCreativeRunAccountID(ctx context.Context, runID string, accountID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return service.ErrCreativeRunNotFound
	}
	if accountID > 0 {
		run.AccountID = &accountID
	}
	return nil
}

func (r *smokeFakeRunRepo) MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return service.ErrCreativeRunNotFound
	}
	if run.Status != service.CreativeRunStatusRunning && run.Status != service.CreativeRunStatusProviderSucceeded && run.Status != service.CreativeRunStatusSettlementPending {
		return service.ErrCreativeInvalidTransition
	}
	run.Status = service.CreativeRunStatusSucceeded
	run.ActualCost = &actualCost
	run.CompletedAt = &now
	return nil
}

func (r *smokeFakeRunRepo) UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error {
	for _, output := range r.outputs[runID] {
		if output.OutputIndex == outputIndex {
			if output.Status == service.CreativeRunOutputStatusAcked {
				return nil
			}
			output.Status = status
			output.MimeType = &mimeType
			output.ByteSize = &byteSize
			output.TransientExpiresAt = transientExpiresAt
			return nil
		}
	}
	return service.ErrCreativeOutputNotFound
}

func (r *smokeFakeRunRepo) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*service.CreativeRunOutput, error) {
	for _, output := range r.outputs[runID] {
		if output.OutputIndex == outputIndex {
			return output, nil
		}
	}
	return nil, service.ErrCreativeOutputNotFound
}

func (r *smokeFakeRunRepo) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*service.CreativeRunOutput, error) {
	return r.outputs[runID], nil
}

func (r *smokeFakeRunRepo) MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error {
	output, err := r.GetCreativeRunOutput(ctx, runID, outputIndex)
	if err != nil {
		return err
	}
	if output.Status == service.CreativeRunOutputStatusAcked {
		return nil
	}
	if output.Status != service.CreativeRunOutputStatusSucceeded {
		return service.ErrCreativeOutputNotReady
	}
	output.Status = service.CreativeRunOutputStatusAcked
	output.AckedAt = &now
	return nil
}

func (r *smokeFakeRunRepo) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*service.CreativeRun, error) {
	return nil, nil
}

func (r *smokeFakeRunRepo) IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, service.ErrCreativeRunNotFound
	}
	run.AttemptCount++
	return run.AttemptCount, nil
}

func (r *smokeFakeRunRepo) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, service.ErrCreativeRunNotFound
	}
	run.SettlementAttemptCount++
	return run.SettlementAttemptCount, nil
}

func (r *smokeFakeRunRepo) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, service.ErrCreativeRunNotFound
	}
	run.ReleaseAttemptCount++
	return run.ReleaseAttemptCount, nil
}

func (r *smokeFakeRunRepo) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	run, ok := r.runs[runID]
	if !ok {
		return service.ErrCreativeRunNotFound
	}
	run.ProvisioningPhase = phase
	return nil
}

func (r *smokeFakeRunRepo) MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, accountID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return service.ErrCreativeRunNotFound
	}
	if accountID > 0 {
		run.AccountID = &accountID
	}
	run.ProviderResultRecordedAt = &now
	if run.Status == service.CreativeRunStatusRunning {
		run.Status = service.CreativeRunStatusProviderSucceeded
	}
	return nil
}

func (r *smokeFakeRunRepo) SetCreativeRunReconcileError(ctx context.Context, runID, message string, next time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return service.ErrCreativeRunNotFound
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

func (r *smokeFakeBillingRepo) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	return nil, nil
}

func (r *smokeFakeBillingRepo) ReserveBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return &service.BatchImageBalanceHoldResult{Applied: true, HoldAmountUSD: cmd.HoldAmount, EstimatedAmountUSD: cmd.HoldAmount, BalanceAmountUSD: cmd.HoldAmount}, nil
}

func (r *smokeFakeBillingRepo) CaptureBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	r.captureN++
	return &service.BatchImageBalanceHoldResult{Applied: true, ActualAmountUSD: cmd.ActualBaseAmountUSD}, nil
}

func (r *smokeFakeBillingRepo) ReleaseBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	r.releaseN++
	return &service.BatchImageBalanceHoldResult{Applied: true}, nil
}

// smokeFakeExecutor 返回固定输出。
type smokeFakeExecutor struct{}

func (e *smokeFakeExecutor) Prepare(ctx context.Context, run service.CreativeRun) (*service.CreativeExecution, error) {
	return &service.CreativeExecution{
		Account:       &service.Account{ID: 55, Platform: service.PlatformGemini},
		UpstreamModel: run.Model,
		ReleaseFunc:   func() {},
	}, nil
}

func (e *smokeFakeExecutor) Execute(ctx context.Context, run service.CreativeRun, payload service.CreativeRunPayload, execution *service.CreativeExecution) (*service.CreativeExecuteResult, error) {
	return &service.CreativeExecuteResult{
		Outputs:   []service.CreativeOutput{{Index: 0, Bytes: []byte("smoke-image-bytes"), Mime: "image/png"}},
		AccountID: 55,
	}, nil
}

func (e *smokeFakeExecutor) IsRetryable(err error) bool { return false }

// smokeFakeManagedKeyRepo 供应固定隐藏 Key。
type smokeFakeManagedKeyRepo struct{ key *service.APIKey }

func (r *smokeFakeManagedKeyRepo) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*service.APIKey, error) {
	if r.key != nil {
		return r.key, nil
	}
	return nil, service.ErrAPIKeyNotFound
}

func (r *smokeFakeManagedKeyRepo) CreateManagedKey(ctx context.Context, key *service.APIKey) error {
	key.ID = 900
	r.key = key
	return nil
}

type smokeFakeUserRepo struct{}

func (r *smokeFakeUserRepo) GetByID(ctx context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id}, nil
}

type smokeFakeGroupRepo struct {
	price1k float64
}

func (r *smokeFakeGroupRepo) GetByIDLite(ctx context.Context, id int64) (*service.Group, error) {
	price := r.price1k
	return &service.Group{
		ID:                   id,
		Name:                 "Smoke Group",
		Platform:             service.PlatformGemini,
		Status:               service.StatusActive,
		AllowImageGeneration: true,
		RateMultiplier:       1,
		ModelPricing:         []service.ChannelModelPricing{{Models: []string{"*"}, BillingMode: service.BillingModeImage, PerRequestPrice: &price}},
	}, nil
}

func (r *smokeFakeGroupRepo) ListActive(ctx context.Context) ([]service.Group, error) {
	return nil, nil
}

type smokeFakeAccountRepo struct{}

func (r *smokeFakeAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]service.Account, error) {
	return []service.Account{{
		ID:          55,
		Status:      service.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gemini-3.1-flash-image": "gemini-3.1-flash-image"},
		},
	}}, nil
}

type smokeFakeRateRepo struct{}

func (r *smokeFakeRateRepo) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	return nil, nil
}

type smokeCreativeSettingReader struct{}

func (smokeCreativeSettingReader) IsCreativeEnabled(context.Context) bool { return true }

func (smokeCreativeSettingReader) GetCreativeModelSettings(context.Context) []service.CreativeModelSetting {
	return []service.CreativeModelSetting{{
		GroupID:    12,
		Model:      "gemini-3.1-flash-image",
		Operations: []string{service.CreativeOperationGenerate, service.CreativeOperationEdit},
	}}
}

// TestCreativeFullChainSmoke 串起创作台全链路（真实 Redis 队列 + 真实 transient store）。
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
	queue := NewCreativeQueue(client, cfg)
	store := NewCreativeTransientStore(client, cfg)
	svc := service.NewCreativePublicService(
		repo,
		&smokeFakeManagedKeyRepo{},
		&smokeFakeUserRepo{},
		&smokeFakeAccountRepo{},
		&smokeFakeGroupRepo{price1k: 0.02},
		&smokeFakeRateRepo{},
		queue,
		store,
		billing,
		nil,
		nil,
		nil,
		nil,
		nil,
		smokeCreativeSettingReader{},
		cfg,
	)
	svc.Pricing = service.NewBillingService(nil, nil)
	worker := service.NewCreativeRunWorker(queue, repo, store, &smokeFakeExecutor{}, svc, service.CreativeWorkerOptions{
		ReserveBlockTimeout: time.Second,
		JobLockTTL:          time.Minute,
		LockConflictDelay:   time.Millisecond,
		ErrorRetryDelay:     time.Millisecond,
		ErrorBackoff:        time.Millisecond,
		StaleActiveAfter:    time.Minute,
		MaxAttempts:         3,
	})
	ctx := context.Background()

	// 1. 创建任务（含源图 + prompt，经校验/审核跳过/估价/预占/暂存/入队）。
	png := smokeTestPNG(t)
	scope := service.CreativeRunScope{UserID: 7, WorkspaceID: "11111111-1111-4111-8111-111111111111"}
	created, err := svc.CreateRun(ctx, scope, service.CreateCreativeRunParamsPublic{
		GroupID:      12,
		Model:        "gemini-3.1-flash-image",
		Operation:    service.CreativeOperationGenerate,
		Prompt:       "smoke prompt",
		SourceImages: []service.CreativeInputImage{{Bytes: png, Mime: "image/png"}},
		ImageSize:    "1K",
	}, "smoke-idem-key")
	require.NoError(t, err)
	require.Equal(t, service.CreativeRunStatusQueued, created.Status)
	runID := created.ID

	// 2. worker 从真实队列 Reserve → 执行 fake provider → 成功结算。
	require.NoError(t, worker.RunOnce(ctx))
	require.Equal(t, service.CreativeRunStatusSucceeded, repo.runs[runID].Status)
	require.Equal(t, 1, billing.captureN)
	require.Equal(t, service.CreativeRunOutputStatusSucceeded, repo.outputs[runID][0].Status)

	// 3. 取回输出字节。
	content, err := svc.GetOutputContent(ctx, scope, runID, 0)
	require.NoError(t, err)
	require.Equal(t, []byte("smoke-image-bytes"), content.Content)
	require.Equal(t, "image/png", content.ContentType)

	// 4. ack 删除临时输出。
	require.NoError(t, svc.AckOutput(ctx, scope, runID, 0))
	require.Equal(t, service.CreativeRunOutputStatusAcked, repo.outputs[runID][0].Status)
	_, err = store.LoadOutput(ctx, runID, 0)
	require.Error(t, err, "ack 后临时输出必须已删除")

	// 5. 再次获取返回 410 语义错误。
	_, err = svc.GetOutputContent(ctx, scope, runID, 0)
	require.ErrorIs(t, err, service.ErrCreativeOutputExpired)
}

// smokeTestPNG 生成一张最小合法 PNG。
func smokeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}
