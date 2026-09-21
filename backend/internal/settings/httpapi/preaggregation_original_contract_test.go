package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/ops"

	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// preAggregationHandlerRepoStub 只记录统一预聚合设置，确保测试不会误用旧配置。
type preAggregationHandlerRepoStub struct {
	values map[string]string
}

func (s *preAggregationHandlerRepoStub) Get(context.Context, string) (*settingscore.Setting, error) {
	return nil, settingscore.ErrSettingNotFound
}

func (s *preAggregationHandlerRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", settingscore.ErrSettingNotFound
	}
	return value, nil
}

func (s *preAggregationHandlerRepoStub) Set(_ context.Context, key, value string) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func (s *preAggregationHandlerRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (s *preAggregationHandlerRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	for key, value := range settings {
		if err := s.Set(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *preAggregationHandlerRepoStub) GetAll(context.Context) (map[string]string, error) {
	result := make(map[string]string, len(s.values))
	for key, value := range s.values {
		result[key] = value
	}
	return result, nil
}

func (s *preAggregationHandlerRepoStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func newPreAggregationHandler(repo settingscore.Repository) *PreAggregationHandler {
	options := &preaggregation.Options{Usage: preaggregation.UsageOptions{Enabled: true, IntervalSeconds: 60, BackfillEnabled: true, BackfillMaxDays: 31}, OpsEnabled: true, OpsAggregationEnabled: true}
	return NewPreAggregationHandler(preaggregation.NewPreAggregationSettingsService(repo, options), nil, nil)

}

// TestSettingHandlerGetPreAggregationSettings 验证旧运维设置不会影响统一配置默认值。
func TestSettingHandlerGetPreAggregationSettings(t *testing.T) {

	repo := &preAggregationHandlerRepoStub{values: map[string]string{
		ops.SettingKeyOpsAdvancedSettings: `{"aggregation":{"aggregation_enabled":false}}`,
		"ops_query_mode_default":          "raw",
	}}
	handler := newPreAggregationHandler(repo)
	router := gin.New()
	router.GET("/pre-aggregation", handler.GetPreAggregationSettings)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pre-aggregation", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Data preAggregationSettingsResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.True(t, envelope.Data.Settings.Usage.Enabled)
	require.Equal(t, 60, envelope.Data.Settings.Usage.IntervalSeconds)
	require.True(t, envelope.Data.Settings.Ops.Enabled)
}

// TestSettingHandlerUpdatePreAggregationSettingsRejectsInvalidInterval 验证接口拒绝越界刷新周期。
func TestSettingHandlerUpdatePreAggregationSettingsRejectsInvalidInterval(t *testing.T) {

	repo := &preAggregationHandlerRepoStub{values: map[string]string{}}
	handler := newPreAggregationHandler(repo)
	router := gin.New()
	router.PUT("/pre-aggregation", handler.UpdatePreAggregationSettings)

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"usage":{"enabled":true,"interval_seconds":10},"ops":{"enabled":true}}`)
	request := httptest.NewRequest(http.MethodPut, "/pre-aggregation", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	_, persisted := repo.values[preaggregation.SettingKeyPreAggregationSettings]
	require.False(t, persisted)
}

// TestSettingHandlerUpdatePreAggregationSettingsPersistsUnifiedValue 验证接口只写入统一配置键。
func TestSettingHandlerUpdatePreAggregationSettingsPersistsUnifiedValue(t *testing.T) {

	repo := &preAggregationHandlerRepoStub{values: map[string]string{}}
	handler := newPreAggregationHandler(repo)
	router := gin.New()
	router.PUT("/pre-aggregation", handler.UpdatePreAggregationSettings)

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"usage":{"enabled":true,"interval_seconds":90},"ops":{"enabled":false}}`)
	request := httptest.NewRequest(http.MethodPut, "/pre-aggregation", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var persisted preaggregation.PreAggregationSettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[preaggregation.SettingKeyPreAggregationSettings]), &persisted))
	require.True(t, persisted.Usage.Enabled)
	require.Equal(t, 90, persisted.Usage.IntervalSeconds)
	require.False(t, persisted.Ops.Enabled)
	require.Len(t, repo.values, 1)
}
