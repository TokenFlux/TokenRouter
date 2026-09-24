package messageforward_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"

	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"

	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayHandleErrorResponse_NoRuleKeepsDefault(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := messageforward.NewRuntime(messageforward.Dependencies{}, messageforward.Options{})
	respBody := []byte(`{"error":{"message":"Invalid schema for field messages"}}`)
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}

	_, err := messageforward.ErrorForTest(svc, context.Background(), httpapi.NewMessageForwardBoundary(c, nil), account, resp, false)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadGateway, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errField["type"])
	assert.Equal(t, "Upstream request failed", errField["message"])
}

func TestGatewayHandleErrorResponse_AppliesRuleFor422(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	ruleSvc := newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{newNonFailoverPassthroughRule(http.StatusUnprocessableEntity, "invalid schema", http.StatusTeapot, "上游请求失败")})
	httpapi.BindErrorPassthroughService(c, ruleSvc)

	svc := messageforward.NewRuntime(messageforward.Dependencies{}, messageforward.Options{})
	respBody := []byte(`{"error":{"message":"Invalid schema for field messages"}}`)
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}

	_, err := messageforward.ErrorForTest(svc, context.Background(), httpapi.NewMessageForwardBoundary(c, nil), account, resp, false)
	require.Error(t, err)
	assert.Equal(t, http.StatusTeapot, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errField["type"])
	assert.Equal(t, "上游请求失败", errField["message"])
}

func TestHandleErrorResponse_SetsResponseCommitted(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := messageforward.NewRuntime(messageforward.Dependencies{}, messageforward.Options{})
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":{"message":"temperature: range: 0..1"}}`))),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 100, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}

	_, err := messageforward.ErrorForTest(svc, context.Background(), httpapi.NewMessageForwardBoundary(c, nil), account, resp, false)
	require.Error(t, err)
	assert.True(t, httpapi.IsResponseCommitted(c), "non-failover error path must mark response committed")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
}

func TestHandleErrorResponse_PassthroughRuleSetsCommitted(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	ruleSvc := newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{
		newNonFailoverPassthroughRule(http.StatusBadRequest, "temperature", http.StatusBadRequest, "参数错误"),
	})
	httpapi.BindErrorPassthroughService(c, ruleSvc)

	svc := messageforward.NewRuntime(messageforward.Dependencies{}, messageforward.Options{})
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":{"message":"temperature: range: 0..1"}}`))),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 200, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}

	_, err := messageforward.ErrorForTest(svc, context.Background(), httpapi.NewMessageForwardBoundary(c, nil), account, resp, false)
	require.Error(t, err)
	assert.True(t, httpapi.IsResponseCommitted(c), "passthrough rule path must mark response committed")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok, "payload[\"error\"] should be map[string]any")
	assert.Equal(t, "参数错误", errField["message"])
}
