package creative_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// TestCreativeCreateRunPersistsOnlyMetadata submits a prompt and source through
// the in-memory service. It inspects creation hashes, pending output metadata
// and readable transient input, describing the fixture calls made during
// submission.
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
	// The repository call receives a digest, and the retained creation parameters
	// let this fixture compare identity without a database content-retention claim.
	require.Equal(t, creative.Sha256Hex([]byte(params.Prompt)), stored.PromptHash)
	require.NotContains(t, stored.PromptHash, "绝不应落库")
	require.NotEmpty(t, stored.RequestFingerprint)
	// Task and output rows contain media metadata.
	require.Equal(t, 1, stored.RequestedOutputCount)
	for _, output := range repo.outputs[created.ID] {
		require.Equal(t, creative.CreativeRunOutputStatusPending, output.Status)
		require.Nil(t, output.MimeType)
		require.Nil(t, output.ByteSize)
	}
	// The transient fixture retains the uploaded bytes for worker input loading;
	// this assertion checks the service write independently from metadata creation.
	_, err = store.LoadInputs(ctx, created.ID, 1)
	require.NoError(t, err)
	require.Empty(t, store.outputs)
}

// TestCreativePricingUsesResolvedPricingConfigPrice quotes the supplied image
// price cards through the shared resolver. It checks flat OpenAI tiers and
// Gemini 512 precedence over the card default, followed by default pricing when
// the tier is absent.
func TestCreativePricingUsesResolvedPricingConfigPrice(t *testing.T) {
	price := 1.0
	resolver := newResolverWithPricingConfig(t, []routing.ModelPricingEntry{{
		Models:          []string{"gpt-image-2"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &price,
	}})
	svc := newCreativeTestService()
	svc.ImageUnitPrice = creativePriceFixture(nil, resolver)
	group := newCreativeTestGroup()
	group.ID = 100

	// Permit floating-point rounding across the flat image-card tier quotes.
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "1K"), 1e-9)
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "2K"), 1e-9)
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "4K"), 1e-9)

	// Gemini 512 prefers a matching price tier, then the price card's default.
	price512 := 0.5
	defaultPrice := 1.25
	resolver = newResolverWithPricingConfig(t, []routing.ModelPricingEntry{{
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

	// Permit floating-point rounding when comparing the selected 512 dollar quote.
	require.InDelta(t, price512, svc.CreativePrice(context.Background(), creativeGroupProjection(geminiGroup), "gemini-3.1-flash-image", "512"), 1e-9)
	resolver = newResolverWithPricingConfig(t, []routing.ModelPricingEntry{{
		Models:          []string{"gemini-3.1-flash-image"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
	}})
	svc.ImageUnitPrice = creativePriceFixture(nil, resolver)
	// Permit floating-point rounding when the card default supplies this quote.
	require.InDelta(t, defaultPrice, svc.CreativePrice(context.Background(), creativeGroupProjection(geminiGroup), "gemini-3.1-flash-image", "512"), 1e-9)
}

// TestCreateRunIdempotency submits same-key replays, a changed prompt and
// another key sequentially. It checks replay identity, conflict classification
// and two distinct creations, with two observed funds reservations and queue
// admissions.
func TestCreateRunIdempotency(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()

	first, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-1")
	require.NoError(t, err)
	require.False(t, first.IdempotentReplay)
	require.Equal(t, creative.CreativeRunStatusQueued, first.Status)
	require.True(t, creative.IsValidCreativeRunID(first.ID))

	// The same key and body return the existing task as a replay.
	replay, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-1")
	require.NoError(t, err)
	require.True(t, replay.IdempotentReplay)
	require.Equal(t, first.ID, replay.ID)

	// Reusing the key for a different body is a conflict.
	conflictParams := validCreateParams()
	conflictParams.Prompt = "完全不同的 prompt"
	_, err = svc.CreateRun(ctx, testCreativeScope(7), conflictParams, "idem-key-1")
	require.ErrorIs(t, err, creative.ErrCreativeRunIdempotencyConflict)

	// A different key creates an independent task for the same body.
	retry, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-2")
	require.NoError(t, err)
	require.False(t, retry.IdempotentReplay)
	require.NotEqual(t, first.ID, retry.ID)

	// Only the two newly created tasks reserve funds and enter the queue.
	require.Equal(t, 2, testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc)).reserveN)
	require.Len(t, testassert.MustType[*creativeFakeQueue](svc.Queue).enqueued, 2)
}

// TestCreativePricingIgnoresQuality creates OpenAI high and low quality tasks
// through one fixture. Their estimates match, the high-quality hold matches its
// estimate, and that task has one pending output row.
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
	// Permit floating-point rounding between the estimated amount and reserved hold.
	require.InDelta(t, highRun.EstimatedCost, highRun.HoldAmount, 1e-9)
	require.Len(t, testassert.MustType[*creativeFakeRunRepo](svc.Repo).outputs[highRun.ID], 1)
}

// TestCreativeEnabledGate varies runtime and process feature switches in
// isolated services. The checked combinations empty or populate listing, and a
// disabled runtime makes creation return the creative-disabled error.
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

// TestCreativeUnpricedModelDoesNotReserve installs a calculator without an
// eligible image price. Listing stays empty and creation returns an error,
// while the fixture funds reservation count remains zero.
func TestCreativeUnpricedModelDoesNotReserve(t *testing.T) {
	svc := newCreativeTestService()
	svc.ImageUnitPrice = creativePriceFixture(billingtestkit.Calculator(nil, nil), nil)
	listed, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, listed.Data)
	_, err = svc.CreateRun(context.Background(), testCreativeScope(7), validCreateParams(), "unpriced-image")
	require.Error(t, err)
	require.Zero(t, testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc)).reserveN)
}

// TestCreativeRejectsClientOnlyGroupBeforeFunding marks the fixture group
// client-restricted and configures a fallback group. Both discovery views stay
// empty and creation returns group-forbidden, with no funds reservation
// observed.
func TestCreativeRejectsClientOnlyGroupBeforeFunding(t *testing.T) {
	svc := newCreativeTestService()
	groups := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)
	groups.byID[12].ClaudeCodeOnly = true
	fallback := int64(99)
	groups.byID[12].FallbackGroupID = &fallback
	groups.active[0] = *groups.byID[12]
	listed, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, listed.Data)
	candidates, err := svc.ListCreativeModelCandidates(context.Background())
	require.NoError(t, err)
	require.Empty(t, candidates)
	_, err = svc.CreateRun(context.Background(), testCreativeScope(7), validCreateParams(), "client-only")
	require.ErrorIs(t, err, creative.ErrCreativeGroupForbidden)
	require.Zero(t, testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc)).reserveN)
}
