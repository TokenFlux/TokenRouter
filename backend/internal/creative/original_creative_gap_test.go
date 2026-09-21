//go:build unit

package creative_test

import (
	"context"
	"errors"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 越权访问：非本人任务必须全部拒绝
// ---------------------------------------------------------------------------

// seedOwnedRun 种入一个属于指定用户的 succeeded 任务（含一张 succeeded 输出）。
func seedOwnedRun(t *testing.T, svc *creative.Public, runID string, userID int64, status string) {
	t.Helper()
	repo := testassert.MustType[*creativeFakeRunRepo](svc.Repo)
	expires := time.Now().Add(30 * time.Minute)
	repo.runs[runID] = &creative.CreativeRun{
		RunID:                runID,
		UserID:               userID,
		WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
		GroupID:              12,
		APIKeyID:             900,
		Model:                "gemini-3.1-flash-image",
		Operation:            creative.CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               status,
		EstimatedCost:        0.02,
	}
	repo.outputs[runID] = []*creative.CreativeRunOutput{
		{
			RunID:              runID,
			OutputIndex:        0,
			Status:             creative.CreativeRunOutputStatusSucceeded,
			MimeType:           creativeStringValuePtr("image/png"),
			ByteSize:           creativeInt64ValuePtr(4),
			TransientExpiresAt: &expires,
		},
	}
}

func creativeStringValuePtr(v string) *string { return &v }

func creativeInt64ValuePtr(v int64) *int64 { return &v }

func TestCreativeOwnershipEnforced(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	runID := "crun_ownerother00001"
	// 任务属于用户 99，当前用户是 7。
	seedOwnedRun(t, svc, runID, 99, creative.CreativeRunStatusSucceeded)
	store := testassert.MustType[*creativeFakeTransient](svc.TransientStore)
	store.outputs[runID+":0"] = []byte("img")

	_, err := svc.GetRun(ctx, testCreativeScope(7), runID)
	require.ErrorIs(t, err, creative.ErrCreativeRunNotFound)

	_, err = svc.GetOutputContent(ctx, testCreativeScope(7), runID, 0)
	require.ErrorIs(t, err, creative.ErrCreativeRunNotFound)

	err = svc.AckOutput(ctx, testCreativeScope(7), runID, 0)
	require.ErrorIs(t, err, creative.ErrCreativeRunNotFound)

	// 本人访问不受影响。
	got, err := svc.GetRun(ctx, testCreativeScope(99), runID)
	require.NoError(t, err)
	require.Equal(t, runID, got.ID)
}

// ---------------------------------------------------------------------------
// 临时结果过期降级
// ---------------------------------------------------------------------------

func TestCreativeGetOutputContentExpiresToResultLost(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	runID := "crun_outputexpired001"
	repo := testassert.MustType[*creativeFakeRunRepo](svc.Repo)
	// succeeded 任务，输出已过期且临时键已不存在。
	past := time.Now().Add(-time.Minute)
	repo.runs[runID] = &creative.CreativeRun{
		RunID:                runID,
		UserID:               7,
		WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
		GroupID:              12,
		APIKeyID:             900,
		Model:                "gemini-3.1-flash-image",
		Operation:            creative.CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               creative.CreativeRunStatusSucceeded,
		EstimatedCost:        0.02,
	}
	repo.outputs[runID] = []*creative.CreativeRunOutput{
		{RunID: runID, OutputIndex: 0, Status: creative.CreativeRunOutputStatusSucceeded, MimeType: creativeStringValuePtr("image/png"), TransientExpiresAt: &past},
	}

	_, err := svc.GetOutputContent(ctx, testCreativeScope(7), runID, 0)
	require.ErrorIs(t, err, creative.ErrCreativeOutputExpired)
	// 成功任务不得伪装成功：必须降级为 result_lost。
	require.Equal(t, creative.CreativeRunStatusResultLost, repo.runs[runID].Status)
}

func TestCreativeGetOutputContentMissingTransientToResultLost(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	runID := "crun_outputmissing001"
	repo := testassert.MustType[*creativeFakeRunRepo](svc.Repo)
	future := time.Now().Add(30 * time.Minute)
	repo.runs[runID] = &creative.CreativeRun{
		RunID:                runID,
		UserID:               7,
		WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
		GroupID:              12,
		APIKeyID:             900,
		Model:                "gemini-3.1-flash-image",
		Operation:            creative.CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               creative.CreativeRunStatusSucceeded,
		EstimatedCost:        0.02,
	}
	repo.outputs[runID] = []*creative.CreativeRunOutput{
		{RunID: runID, OutputIndex: 0, Status: creative.CreativeRunOutputStatusSucceeded, MimeType: creativeStringValuePtr("image/png"), TransientExpiresAt: &future},
	}
	// 注意：临时存储中没有输出字节（worker 丢失 / 已被清理）。

	_, err := svc.GetOutputContent(ctx, testCreativeScope(7), runID, 0)
	require.ErrorIs(t, err, creative.ErrCreativeResultLost)
	require.Equal(t, creative.CreativeRunStatusResultLost, repo.runs[runID].Status)
}

func TestCreativeGetOutputContentSuccess(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	runID := "crun_outputok000000001"
	seedOwnedRun(t, svc, runID, 7, creative.CreativeRunStatusSucceeded)
	store := testassert.MustType[*creativeFakeTransient](svc.TransientStore)
	store.outputs[runID+":0"] = []byte("png-bytes")

	content, err := svc.GetOutputContent(ctx, testCreativeScope(7), runID, 0)
	require.NoError(t, err)
	require.Equal(t, []byte("png-bytes"), content.Content)
	require.Equal(t, "image/png", content.ContentType)
	// 成功读取不得误降级。
	require.Equal(t, creative.CreativeRunStatusSucceeded, testassert.MustType[*creativeFakeRunRepo](svc.Repo).runs[runID].Status)
}

// ---------------------------------------------------------------------------
// 重复结算幂等
// ---------------------------------------------------------------------------

// creativeFakeUsageLogRepo 只记录 Create 调用（内嵌接口使未覆盖方法 panic 于 nil）。
type creativeFakeUsageLogRepo struct {
	usage.UsageLogRepository
	logs []*usage.UsageLog
}

func (r *creativeFakeUsageLogRepo) Create(ctx context.Context, log *usage.UsageLog) (bool, error) {
	r.logs = append(r.logs, log)
	return true, nil
}

func TestCreativeSucceedRunIdempotentSettlement(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	usageRepo := &creativeFakeUsageLogRepo{}
	bindCreativeUsageFixture(svc.Results, usageRepo)
	repo := testassert.MustType[*creativeFakeRunRepo](svc.Repo)
	billing := testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc))

	runID := "crun_settleidempotent1"
	accountID := int64(55)
	repo.runs[runID] = &creative.CreativeRun{
		RunID:                runID,
		UserID:               7,
		WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
		GroupID:              12,
		APIKeyID:             900,
		AccountID:            &accountID,
		Model:                "gemini-3.1-flash-image",
		Operation:            creative.CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               creative.CreativeRunStatusRunning,
		EstimatedCost:        0.02,
		BaseUnitPrice:        0.02,
	}
	repo.outputs[runID] = []*creative.CreativeRunOutput{
		{RunID: runID, OutputIndex: 0, Status: creative.CreativeRunOutputStatusPending},
	}
	results := []creative.ProviderOutput{{Index: 0, Success: true, Bytes: []byte("img"), Mime: "image/png"}}

	first, err := svc.Results.SucceedRun(ctx, runID, accountID, results)
	require.NoError(t, err)
	require.Equal(t, creative.CreativeRunStatusSucceeded, first.Status)

	second, err := svc.Results.SucceedRun(ctx, runID, accountID, results)
	require.NoError(t, err)
	require.Equal(t, creative.CreativeRunStatusSucceeded, second.Status)

	// 捕获与用量日志各只发生一次，且幂等键稳定。
	require.Equal(t, 1, billing.captureN)
	require.Equal(t, []string{"creative_capture:" + runID}, billing.captureIDs)
	require.Len(t, usageRepo.logs, 1)
	require.Equal(t, "creative_settle:"+runID, usageRepo.logs[0].RequestID)
	require.Equal(t, "image", stringValue(usageRepo.logs[0].BillingMode))
	require.Equal(t, 1, usageRepo.logs[0].ImageCount)
	// 输出行保持一条 succeeded，重复结算不产生重复输出。
	outputs := repo.outputs[runID]
	require.Len(t, outputs, 1)
	require.Equal(t, creative.CreativeRunOutputStatusSucceeded, outputs[0].Status)
}

// TestCreativeSucceedRunRequiresTransientOutput 校验任务成功前必须先持久化输出。
func TestCreativeSucceedRunRequiresTransientOutput(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*creative.Public)
		bytes     []byte
	}{
		{
			name: "transient store missing",
			configure: func(svc *creative.Public) {
				bindCreativeTransientFixture(svc, nil)
			},
		},
		{
			name: "save output failed",
			configure: func(svc *creative.Public) {
				testassert.MustType[*creativeFakeTransient](svc.TransientStore).saveOutputErr = errors.New("redis unavailable")
			},
		},
		{
			name:  "empty output",
			bytes: []byte{},
			configure: func(svc *creative.Public) {
				// 使用默认 fake transient，校验空字节在写入前被拒绝。
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := newCreativeTestService()
			repo := testassert.MustType[*creativeFakeRunRepo](svc.Repo)
			accountID := int64(55)
			runID := "crun_transientrequired"
			repo.runs[runID] = &creative.CreativeRun{
				RunID:                runID,
				UserID:               7,
				WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
				GroupID:              12,
				APIKeyID:             900,
				AccountID:            &accountID,
				Model:                "gemini-3.1-flash-image",
				Operation:            creative.CreativeOperationGenerate,
				RequestedOutputCount: 1,
				Status:               creative.CreativeRunStatusRunning,
				EstimatedCost:        0.02,
				BaseUnitPrice:        0.02,
			}
			repo.outputs[runID] = []*creative.CreativeRunOutput{{
				RunID: runID, OutputIndex: 0, Status: creative.CreativeRunOutputStatusPending,
			}}
			test.configure(svc)

			outputBytes := test.bytes
			if outputBytes == nil {
				outputBytes = []byte("image")
			}
			_, err := svc.Results.SucceedRun(context.Background(), runID, accountID, []creative.ProviderOutput{{
				Index: 0, Success: true, Bytes: outputBytes, Mime: "image/png",
			}})

			billingRepo, ok := creativeFixtureBilling(svc).(*creativeFakeBillingRepo)
			require.True(t, ok)
			if test.name == "empty output" {
				require.ErrorIs(t, err, creative.ErrCreativeTransientFailed)
				require.Equal(t, creative.CreativeRunStatusRunning, repo.runs[runID].Status)
				require.Equal(t, creative.CreativeRunOutputStatusPending, repo.outputs[runID][0].Status)
				require.Zero(t, billingRepo.captureN)
			} else {
				// 已发生服务但无法交付时，保持一次计费，绝不能假报 succeeded。
				require.NoError(t, err)
				require.Equal(t, creative.CreativeRunStatusResultLost, repo.runs[runID].Status)
				require.Equal(t, 1, billingRepo.captureN)
			}
		})
	}
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// ---------------------------------------------------------------------------
// PostgreSQL 不保存素材：CreateRun 落库的只有哈希与元数据
// ---------------------------------------------------------------------------

func TestCreativeCreateRunPersistsOnlyMetadata(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	repo := testassert.MustType[*creativeFakeRunRepo](svc.Repo)
	store := testassert.MustType[*creativeFakeTransient](svc.TransientStore)

	params := validCreateParams()
	params.Prompt = "这是一段绝不应落库的 prompt 明文"
	params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 4, 4), Mime: "image/png"}}
	created, err := svc.CreateRun(ctx, testCreativeScope(7), params, "")
	require.NoError(t, err)
	require.True(t, creative.IsValidCreativeRunID(created.ID))

	require.Len(t, repo.createParams, 1)
	stored := repo.createParams[0]
	// prompt 只以 sha256 落库。
	require.Equal(t, creative.Sha256Hex([]byte(params.Prompt)), stored.PromptHash)
	require.NotContains(t, stored.PromptHash, "绝不应落库")
	require.NotEmpty(t, stored.RequestFingerprint)
	// 结构体中不存在任何图片字节/prompt 明文字段，输出行只有元数据。
	require.Equal(t, 1, stored.RequestedOutputCount)
	for _, output := range repo.outputs[created.ID] {
		require.Equal(t, creative.CreativeRunOutputStatusPending, output.Status)
		require.Nil(t, output.MimeType)
		require.Nil(t, output.ByteSize)
	}
	// 图片字节只进入临时存储。
	_, err = store.LoadInputs(ctx, created.ID, 1)
	require.NoError(t, err)
	require.Empty(t, store.outputs)
}

// ---------------------------------------------------------------------------
// ListModels 权限与内容
// ---------------------------------------------------------------------------

func TestCreativeListModelsFiltersAndContent(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	groupRepo := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[

	// 无图片权限的分组不出现在模型列表。
	creativeGroupReader](svc.GroupRepo).source)

	noImage := newCreativeTestGroup()
	noImage.ID = 13
	noImage.AllowImageGeneration = false
	groupRepo.byID[13] = noImage
	groupRepo.active = append(groupRepo.active, *noImage)

	// 不支持的图片平台（anthropic）不出现。
	unsupported := newCreativeTestGroup()
	unsupported.ID = 14
	unsupported.Platform = capability.PlatformAnthropic
	unsupported.Name = "Claude"
	groupRepo.byID[14] = unsupported
	groupRepo.active = append(groupRepo.active, *unsupported)

	got, err := svc.ListModels(ctx, 7)
	require.NoError(t, err)
	require.NotEmpty(t, got.Data)
	for _, item := range got.Data {
		require.Equal(t, int64(12), item.GroupID, "无图片权限或不受支持的分组不得进入模型列表")
		require.Equal(t, []string{"generate", "edit"}, item.Operations)
		require.Equal(t, []string{"512", "1K", "2K", "4K"}, item.ImageSizes)
		require.InDelta(t, 0.02, item.Price1K, 1e-9)
		require.InDelta(t, 0.04, item.Price2K, 1e-9)
		require.Equal(t, "gemini-3.1-flash-image", item.Model)
	}
}

// ---------------------------------------------------------------------------
// ListModels 回退：账号无映射 / 分组无显式图片价
// ---------------------------------------------------------------------------

// TestCreativeListModelsFallbacks 覆盖两类真实部署形态：
// 1) 账号未配置 model_mapping（网关全量透传语义）时应回退平台图片模型候选；
// 2) 分组未显式配置 image_price_* 时应回退平台默认尺寸档位（按默认价计费）。
func TestCreativeListModelsFallbacks(t *testing.T) {
	svc := newCreativeTestService()
	svc.ImageUnitPrice = creativePriceFixture(billingtestkit.Calculator(0, nil, map[string]*pricing.ModelPricing{}), nil)
	ctx := context.Background()
	groupRepo := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)
	accountRepo := testassert.MustType[*creativeFakeAccountRepo](testassert.MustType[

	// openai 分组：无显式图片价、账号无映射 → 候选回退 + GPT Image 2 支持三档尺寸。
	creativeAccountReader](svc.AccountRepo).source)

	openaiGroup := newCreativeTestGroup()
	openaiGroup.ID = 21
	openaiGroup.Name = "ChatGPT Image"
	openaiGroup.Platform = capability.PlatformOpenAI
	openaiGroup.ModelPricing = nil
	groupRepo.byID[21] = openaiGroup
	groupRepo.active = append(groupRepo.active, *openaiGroup)
	accountRepo.byGroup[21] = []accountcore.Record{{
		ID:          61,
		Platform:    capability.PlatformOpenAI,
		Status:      billingcore.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_whitelist": []string{"gpt-image-1", "gpt-image-2"},
		},
	}}

	// gemini 分组：无显式图片价、账号无映射 → 默认候选 + 尺寸回退 ["1K","2K","4K"]。
	geminiGroup := newCreativeTestGroup()
	geminiGroup.ID = 22
	geminiGroup.ModelPricing = nil
	groupRepo.byID[22] = geminiGroup
	groupRepo.active = append(groupRepo.active, *geminiGroup)
	accountRepo.byGroup[22] = []accountcore.Record{{
		ID:          62,
		Platform:    capability.PlatformGemini,
		Status:      billingcore.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_whitelist": []string{"gemini-2.5-flash-image", "gemini-3-pro-image", "gemini-3.1-flash-image"},
		},
	}}

	// grok 分组：无显式图片价 → 尺寸回退 ["1K","2K"]，操作支持 generate/edit。
	grokGroup := newCreativeTestGroup()
	grokGroup.ID = 23
	grokGroup.Name = "Grok Imagine"
	grokGroup.Platform = capability.PlatformGrok
	grokGroup.ModelPricing = nil
	groupRepo.byID[23] = grokGroup
	groupRepo.active = append(groupRepo.active, *grokGroup)
	accountRepo.byGroup[23] = []accountcore.Record{{
		ID:          63,
		Platform:    capability.PlatformGrok,
		Status:      billingcore.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-1.0", "grok-imagine-image-2.0"},
		},
	}}

	// openai 分组：显式只配 1K 价 → 尺寸只返回 ["1K"]（显式配置优先），模型来自映射。
	pricedGroup := newCreativeTestGroup()
	pricedGroup.ID = 24
	pricedGroup.Name = "GPT Image Priced"
	pricedGroup.Platform = capability.PlatformOpenAI
	price1k := 0.02
	pricedGroup.ModelPricing = testImageModelPricing(map[string]*float64{"1K": &price1k})
	groupRepo.byID[24] = pricedGroup
	groupRepo.active = append(groupRepo.active, *pricedGroup)
	accountRepo.byGroup[24] = []accountcore.Record{{
		ID:          64,
		Platform:    capability.PlatformOpenAI,
		Status:      billingcore.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"},
		},
	}}
	testassert.MustType[*creativeFakeSettingReader](svc.Settings).models = append(testassert.MustType[*creativeFakeSettingReader](svc.Settings).models, creative.CreativeModelSetting{GroupID: 21, Model: "gpt-image-1", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit, creative.CreativeOperationInpaint}},
		creative.CreativeModelSetting{GroupID: 21, Model: "gpt-image-2", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit, creative.CreativeOperationInpaint}},
		creative.CreativeModelSetting{GroupID: 22, Model: "gemini-2.5-flash-image", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 22, Model: "gemini-3-pro-image", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 22, Model: "gemini-3.1-flash-image", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 23, Model: "grok-imagine-image-1.0", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 23, Model: "grok-imagine-image-2.0", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 24, Model: "gpt-image-2", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit, creative.CreativeOperationInpaint}},
	)

	got, err := svc.ListModels(ctx, 7)
	require.NoError(t, err)

	byGroup := map[int64][]creative.CreativeModelPublic{}
	for _, item := range got.Data {
		byGroup[item.GroupID] = append(byGroup[item.GroupID], item)
	}

	// openai 无映射回退：两个候选模型、默认价大于 0；GPT Image 2 额外开放 4K。
	require.Len(t, byGroup[21], 2)
	for _, item := range byGroup[21] {
		require.Equal(t, []string{"low", "medium", "high", "auto"}, item.Qualities)
		require.Empty(t, item.OutputFormats)
		require.Nil(t, item.OutputCompression)
		if item.Model == "gpt-image-2" {
			require.Equal(t, []string{"auto", "opaque", "transparent"}, item.BackgroundOptions)
		} else {
			require.Equal(t, []string{"auto", "opaque"}, item.BackgroundOptions)
		}
		require.Equal(t, 1, item.MaxOutputCount)
		require.Equal(t, 16, item.MaxReferenceImages)
		require.Greater(t, item.Price1K, 0.0)
		require.Equal(t, []string{"generate", "edit", "inpaint"}, item.Operations)
	}
	require.Equal(t, []string{"1K", "2K"}, byModel(byGroup[21], "gpt-image-1").ImageSizes)
	require.Equal(t, []string{"1K", "2K", "4K"}, byModel(byGroup[21], "gpt-image-2").ImageSizes)
	require.ElementsMatch(t, []string{"gpt-image-1", "gpt-image-2"},
		[]string{byGroup[21][0].Model, byGroup[21][1].Model})

	// gemini 无映射回退：固定 1K 模型只开放 1K，支持高分辨率的模型开放三档尺寸。
	require.Len(t, byGroup[22], 3)
	require.Equal(t, []string{"1K"}, byModel(byGroup[22], "gemini-2.5-flash-image").ImageSizes)
	require.Equal(t, []string{"1K", "2K", "4K"}, byModel(byGroup[22], "gemini-3-pro-image").ImageSizes)
	flash31 := byModel(byGroup[22], "gemini-3.1-flash-image")
	require.Equal(t, []string{"512", "1K", "2K", "4K"}, flash31.ImageSizes)
	require.Equal(t, []string{"minimal", "high"}, flash31.ThinkingLevels)
	require.Equal(t, 14, flash31.MaxReferenceImages)
	require.ElementsMatch(t, []string{"gemini-2.5-flash-image", "gemini-3-pro-image", "gemini-3.1-flash-image"},
		[]string{byGroup[22][0].Model, byGroup[22][1].Model, byGroup[22][2].Model})

	// grok 回退：1K/2K 档 + generate/edit。
	require.Len(t, byGroup[23], 2)
	grok1 := byModel(byGroup[23], "grok-imagine-image-1.0")
	require.Equal(t, []string{"1K", "2K"}, grok1.ImageSizes)
	require.Empty(t, grok1.Qualities)
	require.Equal(t, []string{"generate", "edit"}, grok1.Operations)
	grok2 := byModel(byGroup[23], "grok-imagine-image-2.0")
	require.Equal(t, []string{"low", "medium"}, grok2.Qualities)
	require.Contains(t, grok2.AspectRatios, "21:9")
	require.Contains(t, grok2.AspectRatios, "5:2")
	require.Contains(t, grok2.AspectRatios, "auto")
	require.Equal(t, 1, grok2.MaxOutputCount)
	require.Equal(t, 3, grok2.MaxReferenceImages)

	// GPT Image 2 即使未配置 4K 覆盖价，也开放 4K 并回退默认价格。
	require.Len(t, byGroup[24], 1)
	require.Equal(t, "gpt-image-2", byGroup[24][0].Model)
	require.Equal(t, []string{"1K", "2K", "4K"}, byGroup[24][0].ImageSizes)
	require.InDelta(t, 0.02, byGroup[24][0].Price1K, 1e-9)
}

func byModel(models []creative.CreativeModelPublic, model string) creative.CreativeModelPublic {
	for _, item := range models {
		if item.Model == model {
			return item
		}
	}
	return creative.CreativeModelPublic{}
}

func TestCreativeFilterImageSizesForModel(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		model    string
		input    []string
		want     []string
	}{
		{
			name:     "gemini 2.5 image is 1K only",
			platform: capability.PlatformGemini,
			model:    "gemini-2.5-flash-image-preview",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K"},
		},
		{
			name:     "gemini lite image is 1K only",
			platform: capability.PlatformGemini,
			model:    "models/gemini-3.1-flash-lite-image",
			input:    []string{"1K", "4K"},
			want:     []string{"1K"},
		},
		{
			name:     "gemini 3 pro keeps configured tiers",
			platform: capability.PlatformGemini,
			model:    "gemini-3-pro-image",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K", "4K"},
		},
		{
			name:     "gemini 3.1 flash prepends 512 tier",
			platform: capability.PlatformGemini,
			model:    "gemini-3.1-flash-image",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"512", "1K", "2K", "4K"},
		},
		{
			name:     "custom model keeps configured tiers",
			platform: capability.PlatformGemini,
			model:    "custom-image-model",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K", "4K"},
		},
		{
			name:     "non gemini is unchanged",
			platform: capability.PlatformOpenAI,
			model:    "gpt-image-2",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K", "4K"},
		},
		{
			name:     "grok image models only expose 1K and 2K",
			platform: capability.PlatformGrok,
			model:    "grok-imagine-image-2.0",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K"},
		},
		{
			name:     "older gpt image models do not expose 4K",
			platform: capability.PlatformOpenAI,
			model:    "gpt-image-1",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, creative.CreativeFilterImageSizesForModel(tt.platform, tt.model, tt.input))
		})
	}
}

// TestCreativePricingUsesResolvedChannelPrice 校验创作台与模型广场共用渠道图片定价。
func TestCreativePricingUsesResolvedChannelPrice(t *testing.T) {
	price := 1.0
	resolver := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:        capability.PlatformOpenAI,
		Models:          []string{"gpt-image-2"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &price,
	}})
	svc := newCreativeTestService()
	svc.ImageUnitPrice = creativePriceFixture(nil, resolver)
	group := newCreativeTestGroup()
	group.ID = 100
	group.Platform = capability.PlatformOpenAI
	group.ModelPricing = nil

	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "1K"), 1e-9)
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "2K"), 1e-9)
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "4K"), 1e-9)

	// Gemini 512 优先匹配渠道自定义 tier；未配置 512 时回退渠道默认价格。
	price512 := 0.5
	defaultPrice := 1.25
	resolver = newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:        capability.PlatformGemini,
		Models:          []string{"gemini-3.1-flash-image"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
		Intervals: []routing.PricingInterval{{
			TierLabel:       "512",
			PerRequestPrice: &price512,
		}},
	}})
	svc.ImageUnitPrice = creativePriceFixture(nil, resolver)
	geminiGroup := newCreativeTestGroup()
	geminiGroup.ID = 100
	geminiGroup.ModelPricing = nil
	require.InDelta(t, price512, svc.CreativePrice(context.Background(), creativeGroupProjection(geminiGroup), "gemini-3.1-flash-image", "512"), 1e-9)
	resolver = newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:        capability.PlatformGemini,
		Models:          []string{"gemini-3.1-flash-image"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
	}})
	svc.ImageUnitPrice = creativePriceFixture(nil, resolver)
	require.InDelta(t, defaultPrice, svc.CreativePrice(context.Background(), creativeGroupProjection(geminiGroup), "gemini-3.1-flash-image", "512"), 1e-9)
}

// TestCreativeListRunsIncludesOutputs 校验历史列表携带输出元数据：
// 前端历史组件依赖 outputs 关联本地素材与缺失占位，列表不能只返回任务壳。
func TestCreativeListRunsIncludesOutputs(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	runID := "crun_listoutputs001"
	seedOwnedRun(t, svc, runID, 7, creative.CreativeRunStatusSucceeded)

	got, err := svc.ListRuns(ctx, testCreativeScope(7), creative.CreativeRunFilter{Limit: 20})
	require.NoError(t, err)
	require.Len(t, got.Data, 1)
	require.Len(t, got.Data[0].Outputs, 1)
	require.Equal(t, 0, got.Data[0].Outputs[0].Index)
	require.Equal(t, "image/png", got.Data[0].Outputs[0].MimeType)
}
