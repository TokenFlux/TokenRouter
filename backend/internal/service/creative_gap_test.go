//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 越权访问：非本人任务必须全部拒绝
// ---------------------------------------------------------------------------

// seedOwnedRun 种入一个属于指定用户的 succeeded 任务（含一张 succeeded 输出）。
func seedOwnedRun(t *testing.T, svc *CreativePublicService, runID string, userID int64, status string) {
	t.Helper()
	repo := svc.Repo.(*creativeFakeRunRepo)
	expires := time.Now().Add(30 * time.Minute)
	repo.runs[runID] = &CreativeRun{
		RunID:                runID,
		UserID:               userID,
		WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
		GroupID:              12,
		APIKeyID:             900,
		Model:                "gemini-3.1-flash-image",
		Operation:            CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               status,
		EstimatedCost:        0.02,
	}
	repo.outputs[runID] = []*CreativeRunOutput{
		{
			RunID:              runID,
			OutputIndex:        0,
			Status:             CreativeRunOutputStatusSucceeded,
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
	seedOwnedRun(t, svc, runID, 99, CreativeRunStatusSucceeded)
	store := svc.TransientStore.(*creativeFakeTransient)
	store.outputs[runID+":0"] = []byte("img")

	_, err := svc.GetRun(ctx, testCreativeScope(7), runID)
	require.ErrorIs(t, err, ErrCreativeRunNotFound)

	_, err = svc.GetOutputContent(ctx, testCreativeScope(7), runID, 0)
	require.ErrorIs(t, err, ErrCreativeRunNotFound)

	err = svc.AckOutput(ctx, testCreativeScope(7), runID, 0)
	require.ErrorIs(t, err, ErrCreativeRunNotFound)

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
	repo := svc.Repo.(*creativeFakeRunRepo)
	// succeeded 任务，输出已过期且临时键已不存在。
	past := time.Now().Add(-time.Minute)
	repo.runs[runID] = &CreativeRun{
		RunID:                runID,
		UserID:               7,
		WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
		GroupID:              12,
		APIKeyID:             900,
		Model:                "gemini-3.1-flash-image",
		Operation:            CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               CreativeRunStatusSucceeded,
		EstimatedCost:        0.02,
	}
	repo.outputs[runID] = []*CreativeRunOutput{
		{RunID: runID, OutputIndex: 0, Status: CreativeRunOutputStatusSucceeded, MimeType: creativeStringValuePtr("image/png"), TransientExpiresAt: &past},
	}

	_, err := svc.GetOutputContent(ctx, testCreativeScope(7), runID, 0)
	require.ErrorIs(t, err, ErrCreativeOutputExpired)
	// 成功任务不得伪装成功：必须降级为 result_lost。
	require.Equal(t, CreativeRunStatusResultLost, repo.runs[runID].Status)
}

func TestCreativeGetOutputContentMissingTransientToResultLost(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	runID := "crun_outputmissing001"
	repo := svc.Repo.(*creativeFakeRunRepo)
	future := time.Now().Add(30 * time.Minute)
	repo.runs[runID] = &CreativeRun{
		RunID:                runID,
		UserID:               7,
		WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
		GroupID:              12,
		APIKeyID:             900,
		Model:                "gemini-3.1-flash-image",
		Operation:            CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               CreativeRunStatusSucceeded,
		EstimatedCost:        0.02,
	}
	repo.outputs[runID] = []*CreativeRunOutput{
		{RunID: runID, OutputIndex: 0, Status: CreativeRunOutputStatusSucceeded, MimeType: creativeStringValuePtr("image/png"), TransientExpiresAt: &future},
	}
	// 注意：临时存储中没有输出字节（worker 丢失 / 已被清理）。

	_, err := svc.GetOutputContent(ctx, testCreativeScope(7), runID, 0)
	require.ErrorIs(t, err, ErrCreativeResultLost)
	require.Equal(t, CreativeRunStatusResultLost, repo.runs[runID].Status)
}

func TestCreativeGetOutputContentSuccess(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	runID := "crun_outputok000000001"
	seedOwnedRun(t, svc, runID, 7, CreativeRunStatusSucceeded)
	store := svc.TransientStore.(*creativeFakeTransient)
	store.outputs[runID+":0"] = []byte("png-bytes")

	content, err := svc.GetOutputContent(ctx, testCreativeScope(7), runID, 0)
	require.NoError(t, err)
	require.Equal(t, []byte("png-bytes"), content.Content)
	require.Equal(t, "image/png", content.ContentType)
	// 成功读取不得误降级。
	require.Equal(t, CreativeRunStatusSucceeded, svc.Repo.(*creativeFakeRunRepo).runs[runID].Status)
}

// ---------------------------------------------------------------------------
// 重复结算幂等
// ---------------------------------------------------------------------------

// creativeFakeUsageLogRepo 只记录 Create 调用（内嵌接口使未覆盖方法 panic 于 nil）。
type creativeFakeUsageLogRepo struct {
	UsageLogRepository
	logs []*UsageLog
}

func (r *creativeFakeUsageLogRepo) Create(ctx context.Context, log *UsageLog) (bool, error) {
	r.logs = append(r.logs, log)
	return true, nil
}

func TestCreativeSucceedRunIdempotentSettlement(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	usageRepo := &creativeFakeUsageLogRepo{}
	svc.UsageLogRepo = usageRepo
	repo := svc.Repo.(*creativeFakeRunRepo)
	billing := svc.BillingRepo.(*creativeFakeBillingRepo)

	runID := "crun_settleidempotent1"
	accountID := int64(55)
	repo.runs[runID] = &CreativeRun{
		RunID:                runID,
		UserID:               7,
		WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
		GroupID:              12,
		APIKeyID:             900,
		AccountID:            &accountID,
		Model:                "gemini-3.1-flash-image",
		Operation:            CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               CreativeRunStatusRunning,
		EstimatedCost:        0.02,
		BaseUnitPrice:        0.02,
	}
	repo.outputs[runID] = []*CreativeRunOutput{
		{RunID: runID, OutputIndex: 0, Status: CreativeRunOutputStatusPending},
	}
	results := []CreativeOutputResult{{Index: 0, Success: true, Bytes: []byte("img"), Mime: "image/png"}}

	first, err := svc.SucceedRun(ctx, runID, accountID, results)
	require.NoError(t, err)
	require.Equal(t, CreativeRunStatusSucceeded, first.Status)

	second, err := svc.SucceedRun(ctx, runID, accountID, results)
	require.NoError(t, err)
	require.Equal(t, CreativeRunStatusSucceeded, second.Status)

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
	require.Equal(t, CreativeRunOutputStatusSucceeded, outputs[0].Status)
}

// TestCreativeSucceedRunRequiresTransientOutput 校验任务成功前必须先持久化输出。
func TestCreativeSucceedRunRequiresTransientOutput(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*CreativePublicService)
		bytes     []byte
	}{
		{
			name: "transient store missing",
			configure: func(svc *CreativePublicService) {
				svc.TransientStore = nil
			},
		},
		{
			name: "save output failed",
			configure: func(svc *CreativePublicService) {
				svc.TransientStore.(*creativeFakeTransient).saveOutputErr = errors.New("redis unavailable")
			},
		},
		{
			name:  "empty output",
			bytes: []byte{},
			configure: func(svc *CreativePublicService) {
				// 使用默认 fake transient，校验空字节在写入前被拒绝。
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := newCreativeTestService()
			repo := svc.Repo.(*creativeFakeRunRepo)
			accountID := int64(55)
			runID := "crun_transientrequired"
			repo.runs[runID] = &CreativeRun{
				RunID:                runID,
				UserID:               7,
				WorkspaceID:          creativeStringValuePtr(testCreativeWorkspaceID),
				GroupID:              12,
				APIKeyID:             900,
				AccountID:            &accountID,
				Model:                "gemini-3.1-flash-image",
				Operation:            CreativeOperationGenerate,
				RequestedOutputCount: 1,
				Status:               CreativeRunStatusRunning,
				EstimatedCost:        0.02,
				BaseUnitPrice:        0.02,
			}
			repo.outputs[runID] = []*CreativeRunOutput{{
				RunID: runID, OutputIndex: 0, Status: CreativeRunOutputStatusPending,
			}}
			test.configure(svc)

			outputBytes := test.bytes
			if outputBytes == nil {
				outputBytes = []byte("image")
			}
			_, err := svc.SucceedRun(context.Background(), runID, accountID, []CreativeOutputResult{{
				Index: 0, Success: true, Bytes: outputBytes, Mime: "image/png",
			}})

			require.ErrorIs(t, err, ErrCreativeTransientFailed)
			require.Equal(t, CreativeRunStatusRunning, repo.runs[runID].Status)
			require.Equal(t, CreativeRunOutputStatusPending, repo.outputs[runID][0].Status)
			require.Equal(t, 0, svc.BillingRepo.(*creativeFakeBillingRepo).captureN)
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
	repo := svc.Repo.(*creativeFakeRunRepo)
	store := svc.TransientStore.(*creativeFakeTransient)

	params := validCreateParams()
	params.Prompt = "这是一段绝不应落库的 prompt 明文"
	params.SourceImages = []CreativeInputImage{{Bytes: makeTestPNG(t, 4, 4), Mime: "image/png"}}
	created, err := svc.CreateRun(ctx, testCreativeScope(7), params, "")
	require.NoError(t, err)
	require.True(t, IsValidCreativeRunID(created.ID))

	require.Len(t, repo.createParams, 1)
	stored := repo.createParams[0]
	// prompt 只以 sha256 落库。
	require.Equal(t, sha256Hex([]byte(params.Prompt)), stored.PromptHash)
	require.NotContains(t, stored.PromptHash, "绝不应落库")
	require.NotEmpty(t, stored.RequestFingerprint)
	// 结构体中不存在任何图片字节/prompt 明文字段，输出行只有元数据。
	require.Equal(t, 1, stored.RequestedOutputCount)
	for _, output := range repo.outputs[created.ID] {
		require.Equal(t, CreativeRunOutputStatusPending, output.Status)
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
	groupRepo := svc.GroupRepo.(*creativeFakeGroupRepo)

	// 无图片权限的分组不出现在模型列表。
	noImage := newCreativeTestGroup()
	noImage.ID = 13
	noImage.AllowImageGeneration = false
	groupRepo.byID[13] = noImage
	groupRepo.active = append(groupRepo.active, *noImage)

	// 不支持的图片平台（anthropic）不出现。
	unsupported := newCreativeTestGroup()
	unsupported.ID = 14
	unsupported.Platform = PlatformAnthropic
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
	svc.Pricing = newBillingServiceWithPrices(nil, nil, map[string]*ModelPricing{})
	ctx := context.Background()
	groupRepo := svc.GroupRepo.(*creativeFakeGroupRepo)
	accountRepo := svc.AccountRepo.(*creativeFakeAccountRepo)

	// openai 分组：无显式图片价、账号无映射 → 候选回退 + GPT Image 2 支持三档尺寸。
	openaiGroup := newCreativeTestGroup()
	openaiGroup.ID = 21
	openaiGroup.Name = "ChatGPT Image"
	openaiGroup.Platform = PlatformOpenAI
	openaiGroup.ModelPricing = nil
	groupRepo.byID[21] = openaiGroup
	groupRepo.active = append(groupRepo.active, *openaiGroup)
	accountRepo.byGroup[21] = []Account{{
		ID:          61,
		Platform:    PlatformOpenAI,
		Status:      StatusActive,
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
	accountRepo.byGroup[22] = []Account{{
		ID:          62,
		Platform:    PlatformGemini,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_whitelist": []string{"gemini-2.5-flash-image", "gemini-3-pro-image", "gemini-3.1-flash-image"},
		},
	}}

	// grok 分组：无显式图片价 → 尺寸回退 ["1K","2K"]，操作支持 generate/edit。
	grokGroup := newCreativeTestGroup()
	grokGroup.ID = 23
	grokGroup.Name = "Grok Imagine"
	grokGroup.Platform = PlatformGrok
	grokGroup.ModelPricing = nil
	groupRepo.byID[23] = grokGroup
	groupRepo.active = append(groupRepo.active, *grokGroup)
	accountRepo.byGroup[23] = []Account{{
		ID:          63,
		Platform:    PlatformGrok,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-1.0", "grok-imagine-image-2.0"},
		},
	}}

	// openai 分组：显式只配 1K 价 → 尺寸只返回 ["1K"]（显式配置优先），模型来自映射。
	pricedGroup := newCreativeTestGroup()
	pricedGroup.ID = 24
	pricedGroup.Name = "GPT Image Priced"
	pricedGroup.Platform = PlatformOpenAI
	price1k := 0.02
	pricedGroup.ModelPricing = testImageModelPricing(map[string]*float64{"1K": &price1k})
	groupRepo.byID[24] = pricedGroup
	groupRepo.active = append(groupRepo.active, *pricedGroup)
	accountRepo.byGroup[24] = []Account{{
		ID:          64,
		Platform:    PlatformOpenAI,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"},
		},
	}}
	svc.Settings.(*creativeFakeSettingReader).models = append(svc.Settings.(*creativeFakeSettingReader).models,
		CreativeModelSetting{GroupID: 21, Model: "gpt-image-1", Operations: []string{CreativeOperationGenerate, CreativeOperationEdit, CreativeOperationInpaint}},
		CreativeModelSetting{GroupID: 21, Model: "gpt-image-2", Operations: []string{CreativeOperationGenerate, CreativeOperationEdit, CreativeOperationInpaint}},
		CreativeModelSetting{GroupID: 22, Model: "gemini-2.5-flash-image", Operations: []string{CreativeOperationGenerate, CreativeOperationEdit}},
		CreativeModelSetting{GroupID: 22, Model: "gemini-3-pro-image", Operations: []string{CreativeOperationGenerate, CreativeOperationEdit}},
		CreativeModelSetting{GroupID: 22, Model: "gemini-3.1-flash-image", Operations: []string{CreativeOperationGenerate, CreativeOperationEdit}},
		CreativeModelSetting{GroupID: 23, Model: "grok-imagine-image-1.0", Operations: []string{CreativeOperationGenerate, CreativeOperationEdit}},
		CreativeModelSetting{GroupID: 23, Model: "grok-imagine-image-2.0", Operations: []string{CreativeOperationGenerate, CreativeOperationEdit}},
		CreativeModelSetting{GroupID: 24, Model: "gpt-image-2", Operations: []string{CreativeOperationGenerate, CreativeOperationEdit, CreativeOperationInpaint}},
	)

	got, err := svc.ListModels(ctx, 7)
	require.NoError(t, err)

	byGroup := map[int64][]CreativeModelPublic{}
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

func byModel(models []CreativeModelPublic, model string) CreativeModelPublic {
	for _, item := range models {
		if item.Model == model {
			return item
		}
	}
	return CreativeModelPublic{}
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
			platform: PlatformGemini,
			model:    "gemini-2.5-flash-image-preview",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K"},
		},
		{
			name:     "gemini lite image is 1K only",
			platform: PlatformGemini,
			model:    "models/gemini-3.1-flash-lite-image",
			input:    []string{"1K", "4K"},
			want:     []string{"1K"},
		},
		{
			name:     "gemini 3 pro keeps configured tiers",
			platform: PlatformGemini,
			model:    "gemini-3-pro-image",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K", "4K"},
		},
		{
			name:     "gemini 3.1 flash prepends 512 tier",
			platform: PlatformGemini,
			model:    "gemini-3.1-flash-image",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"512", "1K", "2K", "4K"},
		},
		{
			name:     "custom model keeps configured tiers",
			platform: PlatformGemini,
			model:    "custom-image-model",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K", "4K"},
		},
		{
			name:     "non gemini is unchanged",
			platform: PlatformOpenAI,
			model:    "gpt-image-2",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K", "4K"},
		},
		{
			name:     "grok image models only expose 1K and 2K",
			platform: PlatformGrok,
			model:    "grok-imagine-image-2.0",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K"},
		},
		{
			name:     "older gpt image models do not expose 4K",
			platform: PlatformOpenAI,
			model:    "gpt-image-1",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, creativeFilterImageSizesForModel(tt.platform, tt.model, tt.input))
		})
	}
}

// TestCreativePricingUsesResolvedChannelPrice 校验创作台与模型广场共用渠道图片定价。
func TestCreativePricingUsesResolvedChannelPrice(t *testing.T) {
	price := 1.0
	resolver := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:        PlatformOpenAI,
		Models:          []string{"gpt-image-2"},
		BillingMode:     BillingModeImage,
		PerRequestPrice: &price,
	}})
	svc := newCreativeTestService()
	svc.PricingResolver = resolver
	group := newCreativeTestGroup()
	group.ID = 100
	group.Platform = PlatformOpenAI
	group.ModelPricing = nil

	require.InDelta(t, 1, svc.creativePrice(context.Background(), group, "gpt-image-2", "1K"), 1e-9)
	require.InDelta(t, 1, svc.creativePrice(context.Background(), group, "gpt-image-2", "2K"), 1e-9)
	require.InDelta(t, 1, svc.creativePrice(context.Background(), group, "gpt-image-2", "4K"), 1e-9)

	// Gemini 512 优先匹配渠道自定义 tier；未配置 512 时回退渠道默认价格。
	price512 := 0.5
	defaultPrice := 1.25
	resolver = newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:        PlatformGemini,
		Models:          []string{"gemini-3.1-flash-image"},
		BillingMode:     BillingModeImage,
		PerRequestPrice: &defaultPrice,
		Intervals: []PricingInterval{{
			TierLabel:       "512",
			PerRequestPrice: &price512,
		}},
	}})
	svc.PricingResolver = resolver
	geminiGroup := newCreativeTestGroup()
	geminiGroup.ID = 100
	geminiGroup.ModelPricing = nil
	require.InDelta(t, price512, svc.creativePrice(context.Background(), geminiGroup, "gemini-3.1-flash-image", "512"), 1e-9)
	resolver = newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:        PlatformGemini,
		Models:          []string{"gemini-3.1-flash-image"},
		BillingMode:     BillingModeImage,
		PerRequestPrice: &defaultPrice,
	}})
	svc.PricingResolver = resolver
	require.InDelta(t, defaultPrice, svc.creativePrice(context.Background(), geminiGroup, "gemini-3.1-flash-image", "512"), 1e-9)
}

// TestCreativeListRunsIncludesOutputs 校验历史列表携带输出元数据：
// 前端历史组件依赖 outputs 关联本地素材与缺失占位，列表不能只返回任务壳。
func TestCreativeListRunsIncludesOutputs(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	runID := "crun_listoutputs001"
	seedOwnedRun(t, svc, runID, 7, CreativeRunStatusSucceeded)

	got, err := svc.ListRuns(ctx, testCreativeScope(7), CreativeRunFilter{Limit: 20})
	require.NoError(t, err)
	require.Len(t, got.Data, 1)
	require.Len(t, got.Data[0].Outputs, 1)
	require.Equal(t, 0, got.Data[0].Outputs[0].Index)
	require.Equal(t, "image/png", got.Data[0].Outputs[0].MimeType)
}
