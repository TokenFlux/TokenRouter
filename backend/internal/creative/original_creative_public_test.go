//go:build unit

package creative_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strconv"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

type creativeFakeRunRepo struct {
	runs         map[string]*creative.CreativeRun
	byIdem       map[string]*creative.CreativeRun
	outputs      map[string][]*creative.CreativeRunOutput
	createErr    error
	createParams []creative.CreateCreativeRunParams
	transition   []string
	setAccountN  int
}

const testCreativeWorkspaceID = "11111111-1111-4111-8111-111111111111"

func testCreativeScope(userID int64) creative.CreativeRunScope {
	return creative.CreativeRunScope{UserID: userID, WorkspaceID: testCreativeWorkspaceID}
}

func newCreativeFakeRunRepo() *creativeFakeRunRepo {
	return &creativeFakeRunRepo{
		runs:    make(map[string]*creative.CreativeRun),
		byIdem:  make(map[string]*creative.CreativeRun),
		outputs: make(map[string][]*creative.CreativeRunOutput),
	}
}

func (r *creativeFakeRunRepo) CreateCreativeRun(ctx context.Context, params creative.CreateCreativeRunParams) (*creative.CreativeRun, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	r.createParams = append(r.createParams, params)
	workspaceID := params.WorkspaceID
	run := &creative.CreativeRun{
		RunID:                      params.RunID,
		UserID:                     params.UserID,
		WorkspaceID:                &workspaceID,
		GroupID:                    params.GroupID,
		APIKeyID:                   params.APIKeyID,
		Model:                      params.Model,
		RequestedModel:             params.RequestedModel,
		Operation:                  params.Operation,
		RequestedOutputCount:       params.RequestedOutputCount,
		ImageSize:                  params.ImageSize,
		AspectRatio:                params.AspectRatio,
		ResponseMIMEType:           params.ResponseMIMEType,
		PromptHash:                 params.PromptHash,
		RequestFingerprint:         params.RequestFingerprint,
		IdempotencyKey:             params.IdempotencyKey,
		Status:                     creative.CreativeRunStatusQueued,
		EstimatedCost:              params.EstimatedCost,
		HoldAmount:                 &params.HoldAmount,
		BaseUnitPrice:              params.BaseUnitPrice,
		SubscriptionRateMultiplier: params.SubscriptionRateMultiplier,
		BalanceRateMultiplier:      params.BalanceRateMultiplier,
		PlanGroupRateEnabled:       params.PlanGroupRateEnabled,
		CreatedAt:                  time.Now(),
	}
	r.runs[run.RunID] = run
	if params.IdempotencyKey != nil {
		r.byIdem[workspaceID+":"+*params.IdempotencyKey] = run
	}
	outputs := make([]*creative.CreativeRunOutput, 0, params.RequestedOutputCount)
	for index := 0; index < params.RequestedOutputCount; index++ {
		outputs = append(outputs, &creative.CreativeRunOutput{RunID: run.RunID, OutputIndex: index, Status: creative.CreativeRunOutputStatusPending})
	}
	r.outputs[run.RunID] = outputs
	return run, nil
}

func (r *creativeFakeRunRepo) GetCreativeRunByRunID(ctx context.Context, runID string) (*creative.CreativeRun, error) {
	run, ok := r.runs[runID]
	if !ok {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

func (r *creativeFakeRunRepo) GetCreativeRunByRunIDForOwner(ctx context.Context, scope creative.CreativeRunScope, runID string) (*creative.CreativeRun, error) {
	run, err := r.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.UserID != scope.UserID || run.WorkspaceID == nil || *run.WorkspaceID != scope.WorkspaceID {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

func (r *creativeFakeRunRepo) GetCreativeRunByIdempotencyKey(ctx context.Context, scope creative.CreativeRunScope, key string) (*creative.CreativeRun, error) {
	run, ok := r.byIdem[scope.WorkspaceID+":"+key]
	if !ok || run.UserID != scope.UserID || run.WorkspaceID == nil || *run.WorkspaceID != scope.WorkspaceID {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

func (r *creativeFakeRunRepo) ListCreativeRunsForOwner(ctx context.Context, scope creative.CreativeRunScope, filter creative.CreativeRunFilter) ([]*creative.CreativeRun, error) {
	out := make([]*creative.CreativeRun, 0)
	for _, run := range r.runs {
		if run.UserID == scope.UserID && run.WorkspaceID != nil && *run.WorkspaceID == scope.WorkspaceID {
			out = append(out, run)
		}
	}
	return out, nil
}

func (r *creativeFakeRunRepo) TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts creative.CreativeRunTransitionOptions) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if !creative.CanTransitionCreativeRun(run.Status, toStatus) {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = toStatus
	if opts.ErrorCode != nil {
		run.ErrorCode = opts.ErrorCode
	}
	if opts.ErrorMessage != nil {
		run.ErrorMessage = opts.ErrorMessage
	}
	if opts.ReleaseTargetStatus != "" {
		run.ReleaseTargetStatus = opts.ReleaseTargetStatus
	}
	if toStatus == creative.CreativeRunStatusCancelled && opts.Now != nil {
		run.CancelledAt = opts.Now
	}
	r.transition = append(r.transition, runID+":"+toStatus)
	return nil
}

func (r *creativeFakeRunRepo) MarkCreativeRunRunning(ctx context.Context, runID string, accountID int64, now time.Time) error {
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

func (r *creativeFakeRunRepo) SetCreativeRunAccountID(ctx context.Context, runID string, accountID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if accountID > 0 {
		run.AccountID = &accountID
		r.setAccountN++
	}
	return nil
}

func (r *creativeFakeRunRepo) MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error {
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

func (r *creativeFakeRunRepo) UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error {
	outputs, ok := r.outputs[runID]
	if !ok {
		return creative.ErrCreativeOutputNotFound
	}
	for _, output := range outputs {
		if output.OutputIndex == outputIndex {
			if output.Status == creative.CreativeRunOutputStatusAcked {
				return nil
			}
			output.Status = status
			output.MimeType = &mimeType
			output.ByteSize = &byteSize
			output.TransientExpiresAt = transientExpiresAt
			if errorCode != "" {
				output.ErrorCode = &errorCode
			}
			return nil
		}
	}
	return creative.ErrCreativeOutputNotFound
}

func (r *creativeFakeRunRepo) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*creative.CreativeRunOutput, error) {
	for _, output := range r.outputs[runID] {
		if output.OutputIndex == outputIndex {
			return output, nil
		}
	}
	return nil, creative.ErrCreativeOutputNotFound
}

func (r *creativeFakeRunRepo) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*creative.CreativeRunOutput, error) {
	return r.outputs[runID], nil
}

func (r *creativeFakeRunRepo) MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error {
	output, err := r.GetCreativeRunOutput(ctx, runID, outputIndex)
	if err != nil {
		return err
	}
	if output.Status != creative.CreativeRunOutputStatusSucceeded {
		return creative.ErrCreativeOutputNotReady
	}
	output.Status = creative.CreativeRunOutputStatusAcked
	output.AckedAt = &now
	return nil
}

func (r *creativeFakeRunRepo) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*creative.CreativeRun, error) {
	return nil, nil
}

// IncrementCreativeRunAttempt 模拟原子递增并返回最新值。
func (r *creativeFakeRunRepo) IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.AttemptCount++
	return run.AttemptCount, nil
}

func (r *creativeFakeRunRepo) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.SettlementAttemptCount++
	return run.SettlementAttemptCount, nil
}

func (r *creativeFakeRunRepo) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.ReleaseAttemptCount++
	return run.ReleaseAttemptCount, nil
}

func (r *creativeFakeRunRepo) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	run.ProvisioningPhase = phase
	return nil
}

func (r *creativeFakeRunRepo) MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, accountID int64, now time.Time) error {
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

func (r *creativeFakeRunRepo) SetCreativeRunReconcileError(ctx context.Context, runID, message string, next time.Time) error {
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

type creativeFakeManagedKeyRepo struct {
	key     *apikey.APIKey
	getErr  error
	createN int
}

func (r *creativeFakeManagedKeyRepo) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*apikey.APIKey, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.key != nil {
		return r.key, nil
	}
	return nil, apikey.ErrAPIKeyNotFound
}

func (r *creativeFakeManagedKeyRepo) CreateManagedKey(ctx context.Context, key *apikey.APIKey) error {
	r.createN++
	if key.ID == 0 {
		key.ID = 900 + int64(r.createN)
	}
	r.key = key
	return nil
}

type creativeFakeUserRepo struct {
	user *identity.User
}

func (r *creativeFakeUserRepo) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	if r.user == nil {
		return nil, identity.ErrUserNotFound
	}
	return r.user, nil
}

type creativeFakeGroupRepo struct {
	byID   map[int64]*routing.Group
	active []routing.Group
}

func (r *creativeFakeGroupRepo) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	group, ok := r.byID[id]
	if !ok {
		return nil, creative.ErrCreativeGroupForbidden
	}
	return group, nil
}

func (r *creativeFakeGroupRepo) ListActive(ctx context.Context) ([]routing.Group, error) {
	return r.active, nil
}

type creativeFakeAccountRepo struct {
	byGroup map[int64][]accountcore.Record
}

func (r *creativeFakeAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]accountcore.Record, error) {
	return r.byGroup[groupID], nil
}

type creativeFakeRateRepo struct{}

func (r *creativeFakeRateRepo) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	return nil, nil
}

type creativeFakeBillingRepo struct {
	reserveN   int
	captureN   int
	releaseN   int
	reserveIDs []string
	captureIDs []string
	releaseIDs []string
}

func (r *creativeFakeBillingRepo) Reserve(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	r.reserveN++
	r.reserveIDs = append(r.reserveIDs, cmd.RequestID)
	return &billing.TaskFundsResult{
		Applied:            true,
		HoldAmountUSD:      cmd.HoldAmount,
		EstimatedAmountUSD: cmd.HoldAmount,
		BalanceAmountUSD:   cmd.HoldAmount,
	}, nil
}

func (r *creativeFakeBillingRepo) Capture(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	r.captureN++
	r.captureIDs = append(r.captureIDs, cmd.RequestID)
	return &billing.TaskFundsResult{
		Applied:         true,
		ActualAmountUSD: cmd.ActualBaseAmountUSD,
	}, nil
}

func (r *creativeFakeBillingRepo) Release(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	r.releaseN++
	r.releaseIDs = append(r.releaseIDs, cmd.RequestID)
	return &billing.TaskFundsResult{Applied: true}, nil
}

type creativeFakeQueue struct {
	enqueued     []string
	reserveBatch []string
	acked        []string
	requeued     []string
	locksGranted int
	lastLock     *creativeFakeJobLock
}

// creativeFakeJobLock 记录锁的释放。
type creativeFakeJobLock struct {
	released bool
}

func (l *creativeFakeJobLock) Release(ctx context.Context) error {
	l.released = true
	return nil
}

func (q *creativeFakeQueue) Enqueue(ctx context.Context, runID string) error {
	q.enqueued = append(q.enqueued, runID)
	return nil
}

func (q *creativeFakeQueue) Reserve(ctx context.Context, blockTimeout time.Duration) (creative.ReservedCreativeRun, error) {
	if len(q.reserveBatch) == 0 {
		return creative.ReservedCreativeRun{}, creative.ErrCreativeQueueEmpty
	}
	runID := q.reserveBatch[0]
	q.reserveBatch = q.reserveBatch[1:]
	return creative.ReservedCreativeRun{RunID: runID, LeaseToken: "test-lease"}, nil
}

func (q *creativeFakeQueue) RequeueAfter(ctx context.Context, runID, leaseToken string, delay time.Duration) error {
	q.requeued = append(q.requeued, runID)
	return nil
}

func (q *creativeFakeQueue) Ack(ctx context.Context, runID, leaseToken string) error {
	q.acked = append(q.acked, runID)
	return nil
}

func (q *creativeFakeQueue) Heartbeat(ctx context.Context, runID, leaseToken string) (bool, error) {
	return true, nil
}

func (q *creativeFakeQueue) MoveDueDelayedToReady(ctx context.Context, limit int) (int, error) {
	return 0, nil
}

func (q *creativeFakeQueue) RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error) {
	return 0, nil
}

func (q *creativeFakeQueue) TryAcquireJobLock(ctx context.Context, runID string, ttl time.Duration) (creative.CreativeRunJobLock, bool, error) {
	lock := &creativeFakeJobLock{}
	q.locksGranted++
	q.lastLock = lock
	return lock, true, nil
}

type creativeFakeTransient struct {
	payloads      map[string]*creative.CreativeRunPayload
	inputs        map[string][]byte
	masks         map[string][]byte
	outputs       map[string][]byte
	saveOutputErr error
}

func newCreativeFakeTransient() *creativeFakeTransient {
	return &creativeFakeTransient{
		payloads: make(map[string]*creative.CreativeRunPayload),
		inputs:   make(map[string][]byte),
		masks:    make(map[string][]byte),
		outputs:  make(map[string][]byte),
	}
}

func (s *creativeFakeTransient) SavePayload(ctx context.Context, runID string, payload *creative.CreativeRunPayload) error {
	s.payloads[runID] = payload
	return nil
}

func (s *creativeFakeTransient) LoadPayload(ctx context.Context, runID string) (*creative.CreativeRunPayload, error) {
	payload, ok := s.payloads[runID]
	if !ok {
		return nil, creative.ErrCreativeTransientFailed
	}
	return payload, nil
}

func (s *creativeFakeTransient) SaveInput(ctx context.Context, runID string, idx int, data []byte) error {
	s.inputs[fmtInputKey(runID, idx)] = data
	return nil
}

func (s *creativeFakeTransient) LoadInputs(ctx context.Context, runID string, count int) ([][]byte, error) {
	out := make([][]byte, 0, count)
	for idx := 0; idx < count; idx++ {
		data, ok := s.inputs[fmtInputKey(runID, idx)]
		if !ok {
			return nil, creative.ErrCreativeTransientFailed
		}
		out = append(out, data)
	}
	return out, nil
}

func (s *creativeFakeTransient) SaveMask(ctx context.Context, runID string, data []byte) error {
	s.masks[runID] = data
	return nil
}

func (s *creativeFakeTransient) LoadMask(ctx context.Context, runID string) ([]byte, error) {
	data, ok := s.masks[runID]
	if !ok {
		return nil, creative.ErrCreativeTransientFailed
	}
	return data, nil
}

func (s *creativeFakeTransient) SaveOutput(ctx context.Context, runID string, index int, data []byte, ttl time.Duration) error {
	if s.saveOutputErr != nil {
		return s.saveOutputErr
	}
	s.outputs[fmtInputKey(runID, index)] = data
	return nil
}

func (s *creativeFakeTransient) LoadOutput(ctx context.Context, runID string, index int) ([]byte, error) {
	data, ok := s.outputs[fmtInputKey(runID, index)]
	if !ok {
		return nil, creative.ErrCreativeTransientFailed
	}
	return data, nil
}

func (s *creativeFakeTransient) DeleteOutput(ctx context.Context, runID string, index int) error {
	delete(s.outputs, fmtInputKey(runID, index))
	return nil
}

func (s *creativeFakeTransient) DeleteRunTransient(ctx context.Context, runID string, inputCount, outputCount int) error {
	delete(s.payloads, runID)
	delete(s.masks, runID)
	return nil
}

func fmtInputKey(runID string, idx int) string {
	return runID + ":" + strconv.Itoa(idx)
}

// ---------------------------------------------------------------------------
// 测试场景
// ---------------------------------------------------------------------------

func newCreativeTestGroup() *routing.Group {
	price1k := 0.02
	price2k := 0.04
	return &routing.Group{
		ID:                   12,
		Name:                 "Gemini Image",
		Platform:             capability.PlatformGemini,
		Status:               billing.StatusActive,
		AllowImageGeneration: true,
		RateMultiplier:       1,
		ModelPricing:         testImageModelPricing(map[string]*float64{"1K": &price1k, "2K": &price2k}),
	}
}

func newCreativeTestAccountRepo() *creativeFakeAccountRepo {
	return &creativeFakeAccountRepo{
		byGroup: map[int64][]accountcore.Record{
			12: {
				{
					ID:          55,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"gemini-3.1-flash-image": "gemini-3.1-flash-image",
						},
					},
				},
			},
		},
	}
}

func newCreativeTestService() *creative.Public {
	group := newCreativeTestGroup()
	return newCreativePublicFixture(newCreativeFakeRunRepo(),
		&creativeFakeManagedKeyRepo{},
		&creativeFakeUserRepo{user: &identity.User{ID: 7}},
		newCreativeTestAccountRepo(),
		&creativeFakeGroupRepo{byID: map[int64]*routing.Group{12: group}, active: []routing.Group{*group}},
		&creativeFakeRateRepo{},
		&creativeFakeQueue{}, nil, newCreativeFakeTransient(),
		&creativeFakeBillingRepo{}, nil, billingtestkit.Calculator(0, nil, nil), nil, nil, nil, nil, &creativeFakeSettingReader{enabled: true, models: []creative.CreativeModelSetting{
			{GroupID: 12, Model: "gemini-3.1-flash-image", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
			{GroupID: 12, Model: "grok-imagine", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		}},
		&config.Config{
			Creative: config.CreativeConfig{
				Enabled:                 true,
				TransientTTLSeconds:     1800,
				MaxAssetBytes:           33554432,
				MaxTotalInputBytes:      67108864,
				MaxPromptChars:          8000,
				DefaultResponseMimeType: "image/png",
				DefaultImageSize:        "1K",
			},
			Default: config.DefaultConfig{APIKeyPrefix: "sk-"},
		})
}

func makeTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func validCreateParams() creative.CreateCreativeRunParamsPublic {
	return creative.CreateCreativeRunParamsPublic{
		GroupID:   12,
		Model:     "gemini-3.1-flash-image",
		Operation: creative.CreativeOperationGenerate,
		Prompt:    "画一只猫",
		ImageSize: "1K",
	}
}

// configureOpenAICreativeTestService 将校验夹具切换到 OpenAI，以覆盖仍保留的 PNG inpaint 规则。
func configureOpenAICreativeTestService(svc *creative.Public) {
	group := newCreativeTestGroup()
	group.Name = "OpenAI Image"
	group.Platform = capability.PlatformOpenAI
	testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[group.ID] = group
	testassert.MustType[*creativeFakeAccountRepo](testassert.MustType[creativeAccountReader](svc.AccountRepo).source).byGroup[group.ID] = []accountcore.Record{{
		ID:          57,
		Platform:    capability.PlatformOpenAI,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"}},
	}}
	testassert.MustType[*creativeFakeSettingReader](svc.Settings).models = []creative.CreativeModelSetting{{
		GroupID: group.ID, Model: "gpt-image-2",
		Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit, creative.CreativeOperationInpaint},
	}}
}

// configureGrok2CreativeTestService 将校验夹具切换到支持质量的 Grok 2.0。
func configureGrok2CreativeTestService(svc *creative.Public) {
	group := newCreativeTestGroup()
	group.Name = "Grok Imagine 2"
	group.Platform = capability.PlatformGrok
	testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[group.ID] = group
	testassert.MustType[*creativeFakeAccountRepo](testassert.MustType[creativeAccountReader](svc.AccountRepo).source).byGroup[group.ID] = []accountcore.Record{{
		ID:          58,
		Platform:    capability.PlatformGrok,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine-image-2.0": "grok-imagine-image-2.0"}},
	}}
	testassert.MustType[*creativeFakeSettingReader](svc.Settings).models = []creative.CreativeModelSetting{{
		GroupID: group.ID, Model: "grok-imagine-image-2.0",
		Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit},
	}}
}

// TestValidateCreateParams 覆盖 CreateRun 的参数校验矩阵。
func TestValidateCreateParams(t *testing.T) {
	t.Run("OpenAI 模型参数通过且固定单输出", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.AspectRatio = "16:9"
		params.Quality = "auto"
		params.Background = "transparent"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, 1, validated.OutputCount)
		require.Equal(t, "16:9", validated.AspectRatio)
		require.Equal(t, "auto", validated.Quality)
		require.Equal(t, "transparent", validated.Background)
	})

	t.Run("固定 PNG 输出且不接受输出格式参数", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Background = "transparent"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "1:1", validated.AspectRatio)
		require.Equal(t, "medium", validated.Quality)
		require.Equal(t, "transparent", validated.Background)

		params.OutputCount = 11
		_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("Grok 2.0 质量比例通过且固定单输出", func(t *testing.T) {
		svc := newCreativeTestService()
		configureGrok2CreativeTestService(svc)
		params := validCreateParams()
		params.Model = "grok-imagine-image-2.0"
		params.AspectRatio = "21:9"
		params.Quality = "low"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "low", validated.Quality)
		require.Equal(t, "21:9", validated.AspectRatio)
		require.Equal(t, 1, validated.OutputCount)
	})

	t.Run("支持模型缺省参数自动选择产品默认值", func(t *testing.T) {
		svc := newCreativeTestService()
		configureGrok2CreativeTestService(svc)
		params := validCreateParams()
		params.Model = "grok-imagine-image-2.0"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "auto", validated.AspectRatio)
		require.Equal(t, "medium", validated.Quality)

		svc = newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params = validCreateParams()
		params.Model = "gpt-image-2"
		validated, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "1:1", validated.AspectRatio)
		require.Equal(t, "medium", validated.Quality)
		require.Equal(t, "auto", validated.Background)

		svc = newCreativeTestService()
		params = validCreateParams()
		validated, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "1:1", validated.AspectRatio)
		require.Equal(t, "minimal", validated.ThinkingLevel)
	})

	t.Run("Gemini 3.1 支持 512 和思考强度但保持单输出", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.ImageSize = "512"
		params.AspectRatio = "21:9"
		params.ThinkingLevel = "high"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "512", validated.ImageSize)
		require.Equal(t, "high", validated.ThinkingLevel)

		params.OutputCount = 2
		_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("模型不存在", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Model = "gemini-9.9-unknown"
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidModel)
	})

	t.Run("分组未开启图片生成", func(t *testing.T) {
		svc := newCreativeTestService()
		group := newCreativeTestGroup()
		group.AllowImageGeneration = false
		testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[12] = group
		params := validCreateParams()
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeGroupImageDisabled)
	})

	t.Run("grok 平台不支持 inpaint", func(t *testing.T) {
		svc := newCreativeTestService()
		group := newCreativeTestGroup()
		group.Platform = capability.PlatformGrok
		testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[12] = group
		testassert.MustType[*creativeFakeAccountRepo](testassert.MustType[creativeAccountReader](svc.AccountRepo).source).byGroup[12] = []accountcore.Record{{
			ID:          56,
			Status:      billing.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine": "grok-imagine"}},
		}}
		params := validCreateParams()
		params.Model = "grok-imagine"
		params.Operation = creative.CreativeOperationInpaint
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeOperationUnsupported)
	})

	t.Run("grok edit 支持单图且最多三张源图", func(t *testing.T) {
		svc := newCreativeTestService()
		group := newCreativeTestGroup()
		group.Platform = capability.PlatformGrok
		testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[12] = group
		testassert.MustType[*creativeFakeAccountRepo](testassert.MustType[creativeAccountReader](svc.AccountRepo).source).byGroup[12] = []accountcore.Record{{
			ID: 56, Platform: capability.PlatformGrok, Status: billing.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine": "grok-imagine"}},
		}}
		params := validCreateParams()
		params.Model = "grok-imagine"
		params.Operation = creative.CreativeOperationEdit
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)

		params.SourceImages = []creative.CreativeInputImage{
			{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"},
			{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"},
			{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"},
			{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"},
		}
		_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("prompt 超长", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Prompt = strings.Repeat("a", 9000)
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativePromptTooLong)
	})

	t.Run("非法 MIME", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.SourceImages = []creative.CreativeInputImage{{Bytes: []byte("not-an-image"), Mime: "image/gif"}}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidMime)
	})

	t.Run("单文件超限", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Options.MaxAssetBytes = 16
		params := validCreateParams()
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 4, 4), Mime: "image/png"}}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeAssetTooLarge)
	})

	t.Run("总输入超限", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Options.MaxTotalInputBytes = 100
		params := validCreateParams()
		params.SourceImages = []creative.CreativeInputImage{
			{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"},
			{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"},
		}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInputTooLarge)
	})

	t.Run("generate 参考图数量超限", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.SourceImages = make([]creative.CreativeInputImage, 17)
		for i := range params.SourceImages {
			params.SourceImages[i] = creative.CreativeInputImage{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"}
		}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("inpaint 缺 mask", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Operation = creative.CreativeOperationInpaint
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeMaskRequired)
	})

	t.Run("gemini inpaint 直接拒绝", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Operation = creative.CreativeOperationInpaint
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeOperationUnsupported)
	})

	t.Run("gemini edit 不接受 mask", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Operation = creative.CreativeOperationEdit
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("mask 非 PNG", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Operation = creative.CreativeOperationInpaint
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: []byte{0xFF, 0xD8, 0xFF, 0x00}, Mime: "image/jpeg"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeMaskRequired)
	})

	t.Run("mask 尺寸不一致", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Operation = creative.CreativeOperationInpaint
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 16, 16), Mime: "image/png"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeMaskSizeMismatch)
	})

	t.Run("非 inpaint 不允许 mask", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("edit 必须带源图", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Operation = creative.CreativeOperationEdit
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("合法 inpaint 通过且默认值生效", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Operation = creative.CreativeOperationInpaint
		params.ImageSize = ""
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "1K", validated.ImageSize)
		require.Equal(t, 1, validated.OutputCount)
		require.NotEmpty(t, validated.Fingerprint)
		require.NotEmpty(t, validated.PromptHash)
	})
}

// TestCreateRunIdempotency 覆盖幂等重放与幂等冲突。
func TestCreateRunIdempotency(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()

	first, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-1")
	require.NoError(t, err)
	require.False(t, first.IdempotentReplay)
	require.Equal(t, creative.CreativeRunStatusQueued, first.Status)
	require.True(t, creative.IsValidCreativeRunID(first.ID))

	// 相同 Key + 相同请求体：返回原任务并标记重放。
	replay, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-1")
	require.NoError(t, err)
	require.True(t, replay.IdempotentReplay)
	require.Equal(t, first.ID, replay.ID)

	// 相同 Key + 不同请求体：返回冲突。
	conflictParams := validCreateParams()
	conflictParams.Prompt = "完全不同的 prompt"
	_, err = svc.CreateRun(ctx, testCreativeScope(7), conflictParams, "idem-key-1")
	require.ErrorIs(t, err, creative.ErrCreativeRunIdempotencyConflict)

	// 相同请求体 + 不同 Key：不得冲突（指纹不做全局唯一，允许正常重试）。
	retry, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-2")
	require.NoError(t, err)
	require.False(t, retry.IdempotentReplay)
	require.NotEqual(t, first.ID, retry.ID)

	// 计费预占与入队各发生两次（重放不重复扣费/入队）。
	require.Equal(t, 2, testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc)).reserveN)
	require.Len(t, testassert.MustType[*creativeFakeQueue](svc.Queue).enqueued, 2)
}

// TestCreativePricingIgnoresQuality 校验质量不改价且每次任务固定一张输出。
func TestCreativePricingIgnoresQuality(t *testing.T) {
	svc := newCreativeTestService()
	configureOpenAICreativeTestService(svc)
	high := validCreateParams()
	high.Model = "gpt-image-2"
	high.Quality = "high"
	highRun, err := svc.CreateRun(context.Background(), testCreativeScope(7), high, "quality-high")
	require.NoError(t, err)

	low := high
	low.Quality = "low"
	lowRun, err := svc.CreateRun(context.Background(), testCreativeScope(7), low, "quality-low")
	require.NoError(t, err)

	require.Equal(t, highRun.EstimatedCost, lowRun.EstimatedCost)
	require.InDelta(t, highRun.EstimatedCost, highRun.HoldAmount, 1e-9)
	require.Len(t, testassert.MustType[*creativeFakeRunRepo](svc.Repo).outputs[highRun.ID], 1)
}

// TestCreativeWorkspaceScopeIsolation 校验同一用户的不同浏览器工作区互不可见且幂等键隔离。
func TestCreativeWorkspaceScopeIsolation(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	firstScope := testCreativeScope(7)
	secondScope := creative.CreativeRunScope{UserID: 7, WorkspaceID: "22222222-2222-4222-8222-222222222222"}

	first, err := svc.CreateRun(ctx, firstScope, validCreateParams(), "same-idempotency-key")
	require.NoError(t, err)
	second, err := svc.CreateRun(ctx, secondScope, validCreateParams(), "same-idempotency-key")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)

	firstList, err := svc.ListRuns(ctx, firstScope, creative.CreativeRunFilter{Limit: 20})
	require.NoError(t, err)
	require.Len(t, firstList.Data, 1)
	require.Equal(t, first.ID, firstList.Data[0].ID)

	secondList, err := svc.ListRuns(ctx, secondScope, creative.CreativeRunFilter{Limit: 20})
	require.NoError(t, err)
	require.Len(t, secondList.Data, 1)
	require.Equal(t, second.ID, secondList.Data[0].ID)

	_, err = svc.GetRun(ctx, firstScope, second.ID)
	require.ErrorIs(t, err, creative.ErrCreativeRunNotFound)

	legacyID := "crun_legacy_workspace_hidden"
	testassert.MustType[*creativeFakeRunRepo](svc.Repo).runs[legacyID] = &creative.CreativeRun{RunID: legacyID, UserID: 7}
	legacyList, err := svc.ListRuns(ctx, firstScope, creative.CreativeRunFilter{Limit: 20})
	require.NoError(t, err)
	for _, run := range legacyList.Data {
		require.NotEqual(t, legacyID, run.ID)
	}
	_, err = svc.GetRun(ctx, firstScope, legacyID)
	require.ErrorIs(t, err, creative.ErrCreativeRunNotFound)
	_, err = svc.GetOutputContent(ctx, firstScope, legacyID, 0)
	require.ErrorIs(t, err, creative.ErrCreativeRunNotFound)
	require.ErrorIs(t, svc.AckOutput(ctx, firstScope, legacyID, 0), creative.ErrCreativeRunNotFound)
}

// TestNormalizeCreativeWorkspaceID 校验工作区 header 的缺失、非法与规范化行为。
func TestNormalizeCreativeWorkspaceID(t *testing.T) {
	_, err := creative.NormalizeCreativeWorkspaceID("")
	require.ErrorIs(t, err, creative.ErrCreativeWorkspaceRequired)
	_, err = creative.NormalizeCreativeWorkspaceID("not-a-uuid")
	require.ErrorIs(t, err, creative.ErrCreativeWorkspaceInvalid)
	normalized, err := creative.NormalizeCreativeWorkspaceID("11111111-1111-4111-8111-111111111111")
	require.NoError(t, err)
	require.Equal(t, testCreativeWorkspaceID, normalized)
	scope, err := creative.NormalizeCreativeRunScope(creative.CreativeRunScope{UserID: 7, WorkspaceID: "11111111-1111-4111-8111-111111111111"})
	require.NoError(t, err)
	require.Equal(t, testCreativeWorkspaceID, scope.WorkspaceID)
	_, err = creative.NormalizeCreativeRunScope(creative.CreativeRunScope{UserID: 0, WorkspaceID: testCreativeWorkspaceID})
	require.ErrorIs(t, err, creative.ErrCreativeRunNotFound)
}

// creativeFakeSettingReader 是 CreativeSettingReader 的测试替身。
type creativeFakeSettingReader struct {
	enabled bool
	models  []creative.CreativeModelSetting
}

func (f *creativeFakeSettingReader) IsCreativeEnabled(ctx context.Context) bool {
	return f.enabled
}

func (f *creativeFakeSettingReader) GetCreativeModelSettings(ctx context.Context) []creative.CreativeModelSetting {
	return append([]creative.CreativeModelSetting(nil), f.models...)
}

// TestCreativeEnabledGate 校验数据库运行时开关 creative_enabled 的门控语义：
// 关闭时 ListModels 返回空列表（前端展示"已停用"空态），CreateRun 返回 ErrCreativeDisabled。
func TestCreativeEnabledGate(t *testing.T) {
	t.Run("运行时关闭时 ListModels 返回空列表", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Settings = &creativeFakeSettingReader{enabled: false}

		models, err := svc.ListModels(context.Background(), 7)
		require.NoError(t, err)
		require.NotNil(t, models)
		require.Empty(t, models.Data)
	})

	t.Run("运行时关闭时 CreateRun 拒绝", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Settings = &creativeFakeSettingReader{enabled: false}

		_, err := svc.CreateRun(context.Background(), testCreativeScope(7), validCreateParams(), "")
		require.ErrorIs(t, err, creative.ErrCreativeDisabled)
	})

	t.Run("运行时开启时 ListModels 正常返回", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Settings = &creativeFakeSettingReader{enabled: true, models: []creative.CreativeModelSetting{{
			GroupID: 12, Model: "gemini-3.1-flash-image", Operations: []string{creative.CreativeOperationGenerate},
		}}}

		models, err := svc.ListModels(context.Background(), 7)
		require.NoError(t, err)
		require.NotEmpty(t, models.Data)
	})

	t.Run("进程配置关闭时运行时开关无法打开", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Options.Enabled = false
		svc.Settings = &creativeFakeSettingReader{enabled: true}

		models, err := svc.ListModels(context.Background(), 7)
		require.NoError(t, err)
		require.Empty(t, models.Data)
	})
}

// TestCreativeGeminiNanoBananaCandidates 校验创作台支持 Gemini nano-banana 别名族。
func TestCreativeGeminiNanoBananaCandidates(t *testing.T) {
	for _, model := range []string{"nano-banana-pro", "nano-banana-2", "NANO-BANANA-PRO", "models/nano-banana-2"} {
		require.True(t, creative.IsCreativeGeminiImageModel(model), "模型 %q 应识别为 Gemini 生图模型", model)
		require.True(t, creative.CreativePlatformImageModel(capability.PlatformGemini, model), "模型 %q 应通过执行器图片模型校验", model)
		capabilities := creative.CreativeCapabilitiesForModel(capability.PlatformGemini, model)
		require.NotEmpty(t, capabilities.AspectRatios, "模型 %q 应暴露 Gemini 图片能力", model)
	}
	require.False(t, creative.IsCreativeGeminiImageModel("nano-banana"), "不完整的 nano-banana 名称不应被识别")

	account := &accountcore.Record{Platform: capability.PlatformGemini, Credentials: map[string]any{}}
	models := creativeAccountModelsForTest(t, account)
	require.Contains(t, models, "nano-banana-pro")
	require.Contains(t, models, "nano-banana-2")
}

func TestCreativeModelSettingsFilterAndCreateValidation(t *testing.T) {
	svc := newCreativeTestService()
	svc.Settings = &creativeFakeSettingReader{
		enabled: true,
		models: []creative.CreativeModelSetting{{
			GroupID:    12,
			Model:      "gemini-3.1-flash-image",
			Operations: []string{creative.CreativeOperationEdit},
		}},
	}

	models, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, models.Data, 1)
	require.Equal(t, []string{creative.CreativeOperationEdit}, models.Data[0].Operations)

	params := validCreateParams()
	params.Operation = creative.CreativeOperationGenerate
	_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
	require.ErrorIs(t, err, creative.ErrCreativeOperationUnsupported)

	params.Operation = creative.CreativeOperationEdit
	params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
	_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
	require.NoError(t, err)
}

// 测试存储模拟闭合成功事实操作；真实回滚由 PostgreSQL 集成测试验证。
func (r *creativeFakeRunRepo) RecordProviderOutcome(ctx context.Context, id string, accountID int64, outputs []creative.CreativeRunOutput, now time.Time) error {
	run, err := r.GetCreativeRunByRunID(ctx, id)
	if err != nil {
		return err
	}
	if run.ProviderResultRecordedAt != nil {
		return nil
	}
	for _, output := range outputs {
		if err := r.UpdateCreativeRunOutput(ctx, id, output.OutputIndex, output.Status, creative.CreativeDerefString(output.MimeType), creative.CreativeDerefInt64(output.ByteSize), output.TransientExpiresAt, creative.CreativeDerefString(output.ErrorCode), creative.CreativeDerefString(output.ErrorMessage)); err != nil {
			return err
		}
	}
	return r.MarkCreativeRunProviderSucceeded(ctx, id, accountID, now)
}

func (r *creativeFakeRunRepo) CompleteProviderOutcome(ctx context.Context, id string, cost float64, lost bool, now time.Time) error {
	if err := r.MarkCreativeRunSucceeded(ctx, id, cost, now); err != nil {
		return err
	}
	run := r.runs[id]
	if lost && run.Status != creative.CreativeRunStatusCancelled {
		run.Status = creative.CreativeRunStatusResultLost
	}
	return nil
}
