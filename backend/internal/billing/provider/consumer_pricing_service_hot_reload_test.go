package provider

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	"github.com/stretchr/testify/require"
)

const hotReloadCatalogJSON = `{
	"remote-model": {"litellm_provider": "test", "mode": "chat",
		"input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06}
}`

func hotReloadModelJSON(name string, input, output float64) string {
	return `"` + name + `": {"litellm_provider": "test", "mode": "chat",
		"input_cost_per_token": ` + formatFloat(input) + `, "output_cost_per_token": ` + formatFloat(output) + `}`
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// newHotReloadPricingService 在数据目录里放好目录缓存与 fallback/override 文件并完成首次加载。
// 传空串表示不配置对应文件。
func newHotReloadPricingService(t *testing.T, fallbackJSON, overrideJSON string) *PricingService {
	t.Helper()
	dir := t.TempDir()
	svc := newPricingServiceFixture(pricingServiceFixture{options: Options{}})
	svc.options.DataDir = dir
	require.NoError(t, os.WriteFile(svc.GetPricingFilePath(), []byte(hotReloadCatalogJSON), 0644))
	if fallbackJSON != "" {
		svc.options.FallbackFile = filepath.Join(dir, "fallback.json")
		require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(fallbackJSON), 0644))
	}
	if overrideJSON != "" {
		svc.options.OverrideFile = filepath.Join(dir, "overrides.json")
		require.NoError(t, os.WriteFile(svc.options.OverrideFile, []byte(overrideJSON), 0644))
	}
	require.NoError(t, svc.LoadPricingData(svc.GetPricingFilePath()))
	return svc
}

func TestPricingCustomFilesFingerprint(t *testing.T) {
	svc := newPricingServiceFixture(pricingServiceFixture{options: Options{}})
	require.Empty(t, svc.CustomPricingFilesFingerprint(), "未配置文件返回空串")

	dir := t.TempDir()
	svc.options.FallbackFile = filepath.Join(dir, "fallback.json")
	missing := svc.CustomPricingFilesFingerprint()
	require.NotEmpty(t, missing)
	require.Equal(t, missing, svc.CustomPricingFilesFingerprint(), "同一状态下指纹稳定")

	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{}`), 0644))
	present := svc.CustomPricingFilesFingerprint()
	require.NotEqual(t, missing, present, "文件出现即视为变化")

	svc.options.OverrideFile = filepath.Join(dir, "overrides.json")
	require.NoError(t, os.WriteFile(svc.options.OverrideFile, []byte(`{}`), 0644))
	require.NotEqual(t, present, svc.CustomPricingFilesFingerprint(), "override 内容参与指纹")
}

func TestPricingHotReload_FallbackChangeRebuildsWithoutTouchingSyncAnchor(t *testing.T) {
	svc := newHotReloadPricingService(t, `{`+hotReloadModelJSON("custom-a", 4e-6, 8e-6)+`}`, "")
	require.InDelta(t, 4e-6, svc.Snapshot().Data["custom-a"].InputCostPerToken, 1e-12)
	require.Nil(t, svc.Snapshot().Data["custom-b"])
	require.NotEmpty(t, svc.Snapshot().CustomFilesHash)
	anchor, updated := svc.Snapshot().LocalHash, svc.Snapshot().LastUpdated

	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{`+
		hotReloadModelJSON("custom-a", 5e-6, 8e-6)+`,`+
		hotReloadModelJSON("custom-b", 1e-6, 3e-6)+`}`), 0644))
	svc.ReloadIfCustomFilesChanged()

	require.InDelta(t, 5e-6, svc.Snapshot().Data["custom-a"].InputCostPerToken, 1e-12, "改价即时生效")
	require.NotNil(t, svc.Snapshot().Data["custom-b"], "新模型即时并入")
	require.InDelta(t, 1e-6, svc.Snapshot().Data["remote-model"].InputCostPerToken, 1e-12, "目录条目不受影响")
	require.Equal(t, anchor, svc.Snapshot().LocalHash, "热重载不得改动远程同步锚点")
	require.Equal(t, updated, svc.Snapshot().LastUpdated)
	require.Equal(t, svc.CustomPricingFilesFingerprint(), svc.Snapshot().CustomFilesHash)
}

func TestPricingHotReload_UnchangedFilesSkipRebuild(t *testing.T) {
	svc := newHotReloadPricingService(t,
		`{`+hotReloadModelJSON("custom-a", 4e-6, 8e-6)+`}`,
		`{"remote-model": {"input_cost_per_token": 7e-06}}`)
	mutatePricingFixture(svc, func(data map[string]*pricing.LiteLLMModelPricing) {
		data["sentinel"] = &pricing.LiteLLMModelPricing{}
	})

	svc.ReloadIfCustomFilesChanged()

	require.Contains(t, svc.Snapshot().Data, "sentinel", "指纹未变时不得重建")
}

func TestPricingHotReload_OverrideChangePatchesCatalogAndAddsModels(t *testing.T) {
	svc := newHotReloadPricingService(t, "", `{"remote-model": {"input_cost_per_token": 7e-06}}`)
	require.InDelta(t, 7e-6, svc.Snapshot().Data["remote-model"].InputCostPerToken, 1e-12)

	require.NoError(t, os.WriteFile(svc.options.OverrideFile, []byte(`{
		"remote-model": {"input_cost_per_token": 9e-06},
		`+hotReloadModelJSON("override-new-model", 5e-6, 1e-5)+`}`), 0644))
	svc.ReloadIfCustomFilesChanged()

	require.InDelta(t, 9e-6, svc.Snapshot().Data["remote-model"].InputCostPerToken, 1e-12)
	require.InDelta(t, 2e-6, svc.Snapshot().Data["remote-model"].OutputCostPerToken, 1e-12, "未覆盖字段保持目录值")
	require.NotNil(t, svc.Snapshot().Data["override-new-model"])

	// 清空补丁：目录条目回到原价，补丁新增的模型随之消失。
	require.NoError(t, os.WriteFile(svc.options.OverrideFile, []byte(`{}`), 0644))
	svc.ReloadIfCustomFilesChanged()

	require.InDelta(t, 1e-6, svc.Snapshot().Data["remote-model"].InputCostPerToken, 1e-12)
	require.Nil(t, svc.Snapshot().Data["override-new-model"])
}

func TestPricingHotReload_InvalidFileKeepsCurrentDataUntilFixed(t *testing.T) {
	svc := newHotReloadPricingService(t, `{`+hotReloadModelJSON("custom-a", 4e-6, 8e-6)+`}`, "")
	before := svc.Snapshot().CustomFilesHash

	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{"custom-a": {"input_cost_per_token": `), 0644))
	svc.ReloadIfCustomFilesChanged()
	require.InDelta(t, 4e-6, svc.Snapshot().Data["custom-a"].InputCostPerToken, 1e-12, "半写文件不得替换数据")
	require.Equal(t, before, svc.Snapshot().CustomFilesHash, "指纹不更新，下一轮继续尝试")

	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{`+hotReloadModelJSON("custom-a", 6e-6, 8e-6)+`}`), 0644))
	svc.ReloadIfCustomFilesChanged()
	require.InDelta(t, 6e-6, svc.Snapshot().Data["custom-a"].InputCostPerToken, 1e-12, "文件修好后正常重建")
	require.NotEqual(t, before, svc.Snapshot().CustomFilesHash)
}

// 删除文件等于清空该层：override 补丁撤销、fallback 独有模型消失，都在下一轮比对时生效，
// 且缺失状态被记录，之后不会每轮重建。
func TestPricingHotReload_DeletedFileDropsItsLayer(t *testing.T) {
	svc := newHotReloadPricingService(t,
		`{`+hotReloadModelJSON("custom-a", 4e-6, 8e-6)+`}`,
		`{"remote-model": {"input_cost_per_token": 7e-06}}`)
	require.NotNil(t, svc.Snapshot().Data["custom-a"])
	require.InDelta(t, 7e-6, svc.Snapshot().Data["remote-model"].InputCostPerToken, 1e-12)

	require.NoError(t, os.Remove(svc.options.OverrideFile))
	svc.ReloadIfCustomFilesChanged()
	require.InDelta(t, 1e-6, svc.Snapshot().Data["remote-model"].InputCostPerToken, 1e-12, "删除 override 后目录条目回到原价")
	require.NotNil(t, svc.Snapshot().Data["custom-a"], "fallback 层不受影响")

	require.NoError(t, os.Remove(svc.options.FallbackFile))
	svc.ReloadIfCustomFilesChanged()
	require.Nil(t, svc.Snapshot().Data["custom-a"], "删除 fallback 后其独有模型消失")
	require.InDelta(t, 1e-6, svc.Snapshot().Data["remote-model"].InputCostPerToken, 1e-12, "目录条目不受影响")

	mutatePricingFixture(svc, func(data map[string]*pricing.LiteLLMModelPricing) {
		data["sentinel"] = &pricing.LiteLLMModelPricing{}
	})
	svc.ReloadIfCustomFilesChanged()
	require.Contains(t, svc.Snapshot().Data, "sentinel", "缺失状态已记录，不得每轮重建")

	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{`+hotReloadModelJSON("custom-a", 4e-6, 8e-6)+`}`), 0644))
	svc.ReloadIfCustomFilesChanged()
	require.NotNil(t, svc.Snapshot().Data["custom-a"], "文件重新出现即恢复")
}

type stubPricingRemoteClient struct{ body string }

func (c stubPricingRemoteClient) FetchPricingJSON(context.Context, string) ([]byte, error) {
	return []byte(c.body), nil
}

func (c stubPricingRemoteClient) FetchHashText(context.Context, string) (string, error) {
	return "", nil
}

// 远程下载重建后指纹必须同步到当前文件内容，否则下一轮定时比对会多做一次无意义重载。
func TestPricingHotReload_DownloadRefreshesFingerprint(t *testing.T) {
	svc := newHotReloadPricingService(t, `{`+hotReloadModelJSON("custom-a", 4e-6, 8e-6)+`}`, "")
	svc.options.RemoteURL = "https://example.com/pricing.json"
	setPricingFixtureRemote(svc, stubPricingRemoteClient{body: hotReloadCatalogJSON})
	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{`+hotReloadModelJSON("custom-b", 1e-6, 3e-6)+`}`), 0644))

	require.NoError(t, svc.DownloadPricingData())

	require.NotNil(t, svc.Snapshot().Data["custom-b"])
	require.Nil(t, svc.Snapshot().Data["custom-a"])
	require.Equal(t, svc.CustomPricingFilesFingerprint(), svc.Snapshot().CustomFilesHash)

	mutatePricingFixture(svc, func(data map[string]*pricing.LiteLLMModelPricing) {
		data["sentinel"] = &pricing.LiteLLMModelPricing{}
	})
	svc.ReloadIfCustomFilesChanged()
	require.Contains(t, svc.Snapshot().Data, "sentinel", "下载已消化文件变化，不得再次重建")
}

// 只配置了 fallback/override 而没有 remote_url 时调度器也要运行，否则文件改动无人比对。
func TestPricingSchedulerStartsForCustomFilesWithoutRemoteURL(t *testing.T) {
	svc := NewPricingService(Options{
		RemoteURL:    "",
		FallbackFile: filepath.Join(t.TempDir(), "fallback.json"),
	}, nil)

	svc.StartUpdateScheduler()
	done := make(chan struct{})
	go func() {
		svc.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("custom file watch must keep the scheduler running")
	case <-time.After(50 * time.Millisecond):
	}

	svc.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler must exit after Stop")
	}
}
