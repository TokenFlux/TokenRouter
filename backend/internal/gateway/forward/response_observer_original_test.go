package forward

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/stretchr/testify/require"
)

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
