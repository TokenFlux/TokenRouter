//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newCreativeNoMediaRetentionModerationService 构造带 httptest 审核上游的测试服务。
// 上游对文本单元返回干净分、对图片单元返回命中分；图片以 data URL 内联，
// 使对照组的快照抓取走内联解码（不受 SSRF 回环限制）。
func newCreativeNoMediaRetentionModerationService(t *testing.T) (*ContentModerationService, *contentModerationTestRepo, *contentModerationTestHashCache, string) {
	t.Helper()
	pngBytes := makeTestPNG(t, 2, 2)
	imageDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 文本单元与图片单元是独立的审核调用：按请求体中是否包含 image_url 区分返回。
		var request struct {
			Input any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		rawInput, _ := json.Marshal(request.Input)
		if strings.Contains(string(rawInput), "image_url") {
			_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{
				{CategoryScores: map[string]float64{"sexual": 0.99}},
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{
			{CategoryScores: map[string]float64{"sexual": 0.01}},
		}})
	}))
	t.Cleanup(server.Close)

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModeObserve
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	hashCache := &contentModerationTestHashCache{}
	svc := NewContentModerationService(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo,
		hashCache,
		nil,
		nil,
		nil,
		nil,
	)
	svc.Start()
	return svc, repo, hashCache, imageDataURL
}

// TestContentModerationNoMediaRetention 实验组：
// NoMediaRetention=true 时审核日志只保留元数据，不做媒体快照、不留正文摘录，但仍记录输入 hash。
func TestContentModerationNoMediaRetention(t *testing.T) {
	svc, repo, hashCache, imageDataURL := newCreativeNoMediaRetentionModerationService(t)

	body := []byte(`{"model":"gpt-image-2","prompt":"sexy picture please","images":[{"image_url":"` + imageDataURL + `"}]}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		RequestID:        "creative_mod_test_noretention",
		UserID:           7,
		BillingUserID:    7,
		Endpoint:         "/v1/creative/runs",
		Provider:         "openai",
		Protocol:         ContentModerationProtocolOpenAIImages,
		Body:             body,
		NoMediaRetention: true,
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed, "observe 模式下命中只记录不阻断")

	logs := requireContentModerationLogCount(t, repo, 1)
	log := logs[0]
	require.True(t, log.Flagged)
	require.Equal(t, "sexual", log.HighestCategory)
	// 无媒体留存：正文与媒体字段全部为空，媒体快照记录（含 error 记录）也不得产生。
	require.Empty(t, log.InputExcerpt)
	require.Empty(t, log.InputItems)
	require.Empty(t, log.Media, "无媒体留存模式不得保存媒体快照")
	// 输入 hash 仍要记录（去重前置依赖），分类、分数、决策保留。
	require.NotEmpty(t, hashCache.snapshotRecorded())
	require.NotEmpty(t, log.CategoryScores)
	require.NotEmpty(t, log.Action)
}

// TestContentModerationMediaRetentionControl 对照组：
// NoMediaRetention=false 保持原有行为：媒体快照落库（ready）、正文摘录保留。
func TestContentModerationMediaRetentionControl(t *testing.T) {
	svc, repo, _, imageDataURL := newCreativeNoMediaRetentionModerationService(t)

	body := []byte(`{"model":"gpt-image-2","prompt":"sexy picture please","images":[{"image_url":"` + imageDataURL + `"}]}`)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{
		RequestID:     "creative_mod_test_control",
		UserID:        7,
		BillingUserID: 7,
		Endpoint:      "/v1/creative/runs",
		Provider:      "openai",
		Protocol:      ContentModerationProtocolOpenAIImages,
		Body:          body,
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed)

	logs := requireContentModerationLogCount(t, repo, 1)
	log := logs[0]
	require.NotEmpty(t, log.InputExcerpt, "对照组必须保留正文摘录")
	require.NotEmpty(t, log.InputItems, "对照组必须保留输入项")
	require.Len(t, log.Media, 1, "对照组必须对命中媒体做快照")
	require.Equal(t, "ready", log.Media[0].SnapshotStatus)
	require.NotEmpty(t, log.Media[0].Content, "对照组快照必须包含图片字节")
}
