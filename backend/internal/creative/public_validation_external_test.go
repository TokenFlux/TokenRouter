package creative_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// TestValidateCreateParams exercises model-specific options, product defaults
// and the listed invalid input cases. Assertions inspect normalized controls
// and single-output counts, plus errors for unsupported models/operations,
// limits, MIME and inpaint mask requirements.
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

		testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[12] = group
		testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source).byGroup[12] = []providercore.Record{{
			ID:          56,
			Platform:    capability.PlatformGrok,
			Status:      billing.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine-image-2.0": "grok-imagine-image-2.0"}},
		}}
		params := validCreateParams()
		params.Model = "grok-imagine-image-2.0"
		params.Operation = creative.CreativeOperationInpaint
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeOperationUnsupported)
	})

	t.Run("grok edit 支持单图且最多三张源图", func(t *testing.T) {
		svc := newCreativeTestService()
		group := newCreativeTestGroup()

		testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[12] = group
		testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source).byGroup[12] = []providercore.Record{{
			ID: 56, Platform: capability.PlatformGrok, Status: billing.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine-image-2.0": "grok-imagine-image-2.0"}},
		}}
		params := validCreateParams()
		params.Model = "grok-imagine-image-2.0"
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

// TestCreativeModelSettingsFilterAndCreateValidation permits editing alone for
// one saved Gemini model. Listing advertises edit, generation validation fails,
// and an edit request with its source image succeeds.
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

// TestBuildCreativeRequestFingerprint repeats one supplied normalized payload
// and then changes prompt digest or group. Repeated hashing is equal, while
// each of those two individual changes produces a different observed
// fingerprint.
func TestBuildCreativeRequestFingerprint(t *testing.T) {
	base := creative.CreativeFingerprintPayload{
		GroupID:      12,
		Model:        "gemini-3.1-flash-image",
		Operation:    creative.CreativeOperationGenerate,
		PromptSHA256: creative.Sha256Hex([]byte("hello")),
		ImageSHA256:  []string{creative.Sha256Hex([]byte("img"))},
		ImageSize:    "1K",
		AspectRatio:  "1:1",
		OutputCount:  1,
	}
	first := creative.BuildCreativeRequestFingerprint(base)
	require.NotEmpty(t, first)
	require.Equal(t, first, creative.BuildCreativeRequestFingerprint(base))

	changedPrompt := base
	changedPrompt.PromptSHA256 = creative.Sha256Hex([]byte("world"))
	require.NotEqual(t, first, creative.BuildCreativeRequestFingerprint(changedPrompt))

	changedGroup := base
	changedGroup.GroupID = 13
	require.NotEqual(t, first, creative.BuildCreativeRequestFingerprint(changedGroup))
}

// BenchmarkCreativeConfiguredValidation measures selected-model validation over
// sixty-five fixture providers sharing 186 configured allowlist and mapping
// entries. It resets timing after setup, reports allocations and fails if any
// measured validation returns an error.
func BenchmarkCreativeConfiguredValidation(b *testing.B) {
	service := newCreativeTestService()
	configureOpenAICreativeTestService(service)
	rows := testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](service.ProviderRepo).source)
	// Share the same large rules across many provider records to measure one
	// selected image model against configured candidates after service setup.
	whitelist := make([]any, 186)
	mapping := map[string]any{}
	for i := range whitelist {
		model := fmt.Sprintf("probe-model-%03d", i)
		if i == 0 {
			model = "gpt-image-2"
		}
		whitelist[i] = model
		mapping[model] = model
	}
	records := make([]providercore.Record, 65)
	for i := range records {
		records[i] = providercore.Record{ID: int64(i + 1), Platform: "openai", Type: "apikey", Status: "active", Schedulable: true, Credentials: map[string]any{"model_whitelist": whitelist, "model_mapping": mapping}}
	}
	rows.byGroup[12] = records
	params := validCreateParams()
	params.Model = "gpt-image-2"
	b.ReportAllocs()
	// Exclude fixture assembly from the measured iterations while retaining
	// validation and rule preparation in each timed service call below.
	b.ResetTimer()
	for range b.N {
		if _, err := service.ValidateCreateParams(context.Background(), 7, &params); err != nil {
			b.Fatal(err)
		}
	}
}
