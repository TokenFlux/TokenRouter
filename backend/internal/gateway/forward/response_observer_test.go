package forward

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// TestUpstreamResponseModelObserverServiceTier supplies Responses progress and
// terminal tiers, conflicting Chat tiers, and Anthropic speed changes. It
// protects terminal precedence, unresolved-conflict emptiness, recognized speed
// evidence, and empty results for absent usable observations.
func TestUpstreamResponseModelObserverServiceTier(t *testing.T) {
	t.Run("Responses 终止事件覆盖请求档位回显", func(t *testing.T) {
		observer := &ResponseObserver{}
		observer.ObserveOpenAI([]byte(`{"type":"response.created","response":{"model":"gpt-5.6","service_tier":"priority"}}`), "response.created")
		require.Empty(t, observer.ServiceTier())

		observer.ObserveOpenAI([]byte(`{"type":"response.completed","response":{"model":"gpt-5.6","service_tier":"default"}}`), "response.completed")
		require.Equal(t, "default", observer.ServiceTier())
	})

	t.Run("Chat Completions 档位冲突时不采信", func(t *testing.T) {
		observer := &ResponseObserver{}
		observer.ObserveOpenAI([]byte(`{"model":"gpt-5.6","service_tier":"priority"}`), "")
		observer.ObserveOpenAI([]byte(`{"model":"gpt-5.6","service_tier":"default"}`), "")
		require.Empty(t, observer.ServiceTier())
	})

	t.Run("Chat Completions 终态档位覆盖早期回显", func(t *testing.T) {
		observer := &ResponseObserver{}
		observer.ObserveOpenAI([]byte(`{"model":"gpt-5.6","service_tier":"priority","choices":[{"finish_reason":""}]}`), "")
		terminal := []byte(`{"model":"gpt-5.6","service_tier":"default","choices":[{"finish_reason":"stop"}]}`)
		observer.ObserveOpenAI(terminal, openai.OpenAIChatCompletionServiceTierEventType(terminal))
		require.Equal(t, "default", observer.ServiceTier())
	})

	t.Run("Anthropic usage.speed 可作为实际档位", func(t *testing.T) {
		observer := &ResponseObserver{}
		observer.ObserveAnthropic([]byte(`{"type":"message_start","message":{"model":"claude-opus-5","usage":{"speed":"fast"}}}`))
		require.Equal(t, "fast", observer.ServiceTier())

		observer.ObserveAnthropic([]byte(`{"type":"message_delta","usage":{"speed":"standard"}}`))
		require.Empty(t, observer.ServiceTier())
	})

	t.Run("未知档位和空对象安全降级", func(t *testing.T) {
		observer := &ResponseObserver{}
		observer.ObserveOpenAI([]byte(`{"service_tier":"turbo"}`), "")
		require.Empty(t, observer.ServiceTier())
		var nilObserver *ResponseObserver
		require.Empty(t, nilObserver.ServiceTier())
	})
}

// TestUpstreamResponseModelObserver_ObservesServiceTier contrasts typed progress,
// terminal, untyped Chat, and model-free payloads. Tier evidence requires a
// usable model and excludes nonterminal Responses echoes while retaining
// terminal or untyped declarations.
func TestUpstreamResponseModelObserver_ObservesServiceTier(t *testing.T) {
	t.Parallel()

	observer := &ResponseObserver{}
	// Typed nonterminal events can echo the request tier; wait for terminal evidence.
	observer.ObserveOpenAI([]byte(`{"type":"response.created","response":{"model":"gpt-5.5","service_tier":"flex"}}`), "response.created")
	require.Empty(t, observer.ServiceTier())

	// A terminal declaration with a model takes precedence.
	observer.ObserveOpenAI([]byte(`{"type":"response.completed","response":{"model":"gpt-5.5","service_tier":"default"}}`), "response.completed")
	require.Equal(t, "default", observer.ServiceTier())

	// Chat Completions exposes service_tier in an untyped payload.
	ccObserver := &ResponseObserver{}
	ccObserver.ObserveOpenAI([]byte(`{"id":"chatcmpl-1","model":"gpt-5.5","service_tier":"priority","choices":[]}`), "")
	require.Equal(t, "priority", ccObserver.ServiceTier())

	// A frame without a model does not establish a service tier.
	modelFree := &ResponseObserver{}
	modelFree.ObserveOpenAI([]byte(`{"type":"response.completed","response":{"service_tier":"default"}}`), "response.completed")
	require.Empty(t, modelFree.ServiceTier())
}

// TestOpenAIResponseObserverAcceptsUltrafast retains an observed Ultrafast
// tier as execution evidence. Completion can then compare it with the
// outbound tier without losing the actual tier on the response.
func TestOpenAIResponseObserverAcceptsUltrafast(t *testing.T) {
	observer := &ResponseObserver{}
	observer.ObserveOpenAI([]byte(`{"type":"response.completed","response":{"model":"gpt-6.2-sol","service_tier":"ultrafast"}}`), "response.completed")
	require.Equal(t, "ultrafast", observer.ServiceTier())
}
