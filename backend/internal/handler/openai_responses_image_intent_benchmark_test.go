package handler

import (
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

var openAIResponsesImageIntentRoutingBenchmarkSink account.OpenAIEndpointCapability

func BenchmarkOpenAIResponsesImageIntentRouting_LargeToolsBody(b *testing.B) {
	body := buildLargeOpenAIResponsesToolsBody(32 << 20)
	if service.IsExplicitImageGenerationIntent("/v1/responses", "gpt-5.4", body) {
		b.Fatal("large tools body must not have explicit image intent")
	}
	platform := capability.PlatformOpenAI

	b.Run("reuse_once", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()
		for range b.N {
			imageIntent := service.IsExplicitImageGenerationIntent("/v1/responses", "gpt-5.4", body)
			openAIResponsesImageIntentRoutingBenchmarkSink = openAIResponsesRequiredCapability(imageIntent, platform)
		}
	})

	b.Run("rescan_twice", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()
		for range b.N {
			imageIntent := service.IsExplicitImageGenerationIntent("/v1/responses", "gpt-5.4", body)
			// 对照优化前路径：路由阶段会再次扫描同一份未修改的 body。
			requiredCapability := account.OpenAIEndpointCapabilityTextGeneration
			if service.IsExplicitImageGenerationIntent("/v1/responses", "gpt-5.4", body) && platform == capability.PlatformOpenAI {
				requiredCapability = account.OpenAIEndpointCapabilityResponses
			}
			if imageIntent && requiredCapability != account.OpenAIEndpointCapabilityResponses {
				b.Fatal("explicit image intent must require Responses")
			}
			openAIResponsesImageIntentRoutingBenchmarkSink = requiredCapability
		}
	})
}

func buildLargeOpenAIResponsesToolsBody(targetBytes int) []byte {
	var builder strings.Builder
	builder.Grow(targetBytes + 256)
	_, _ = builder.WriteString(`{"model":"gpt-5.4","tools":[{"type":"function","name":"search","description":"`)
	_, _ = builder.WriteString(strings.Repeat("x", targetBytes))
	_, _ = builder.WriteString(`"}],"tool_choice":"auto","input":"write code"}`)
	return []byte(builder.String())
}
