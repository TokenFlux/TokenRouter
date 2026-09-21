package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	"github.com/stretchr/testify/require"
)

func TestRunProxyQualityTarget_CloudflareChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("cf-ray", "test-ray-123")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<!DOCTYPE html><title>Just a moment...</title><script>window._cf_chl_opt={};</script>"))
	}))
	defer server.Close()

	target := egress.ProxyQualityTarget{
		Target: "openai",
		URL:    server.URL,
		Method: http.MethodGet,
		AllowedStatuses: map[int]struct{}{
			http.StatusUnauthorized: {},
		},
	}

	item := RunProxyQualityTarget(context.Background(), server.Client(), target)
	require.Equal(t, "challenge", item.Status)
	require.Equal(t, http.StatusForbidden, item.HTTPStatus)
	require.Equal(t, "test-ray-123", item.CFRay)
}

func TestRunProxyQualityTarget_AllowedStatusPass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	target := egress.ProxyQualityTarget{
		Target: "gemini",
		URL:    server.URL,
		Method: http.MethodGet,
		AllowedStatuses: map[int]struct{}{
			http.StatusOK: {},
		},
	}

	item := RunProxyQualityTarget(context.Background(), server.Client(), target)
	require.Equal(t, "pass", item.Status)
	require.Equal(t, http.StatusOK, item.HTTPStatus)
}

func TestRunProxyQualityTarget_AllowedStatusPassForUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	target := egress.ProxyQualityTarget{
		Target: "openai",
		URL:    server.URL,
		Method: http.MethodGet,
		AllowedStatuses: map[int]struct{}{
			http.StatusUnauthorized: {},
		},
	}

	item := RunProxyQualityTarget(context.Background(), server.Client(), target)
	require.Equal(t, "pass", item.Status)
	require.Equal(t, http.StatusUnauthorized, item.HTTPStatus)
	require.Contains(t, item.Message, "目标可达")
}

func TestProxyQualityTargets_IncludesGrok(t *testing.T) {
	var grokTarget *egress.ProxyQualityTarget
	for i := range egress.ProxyQualityTargets {
		if egress.ProxyQualityTargets[i].Target == "grok" {
			grokTarget = &egress.ProxyQualityTargets[i]
			break
		}
	}

	require.NotNil(t, grokTarget)
	require.Equal(t, "https://api.x.ai/v1/models", grokTarget.URL)
	require.Equal(t, http.MethodGet, grokTarget.Method)
	require.Contains(t, grokTarget.AllowedStatuses, http.StatusUnauthorized)
}

func TestRunProxyQualityTarget_GrokUnauthorizedPasses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	target := egress.ProxyQualityTarget{
		Target: "grok",
		URL:    server.URL,
		Method: http.MethodGet,
		AllowedStatuses: map[int]struct{}{
			http.StatusUnauthorized: {},
		},
	}

	item := RunProxyQualityTarget(context.Background(), server.Client(), target)
	require.Equal(t, "grok", item.Target)
	require.Equal(t, "pass", item.Status)
	require.Equal(t, http.StatusUnauthorized, item.HTTPStatus)
	require.Contains(t, item.Message, "目标可达")
}
