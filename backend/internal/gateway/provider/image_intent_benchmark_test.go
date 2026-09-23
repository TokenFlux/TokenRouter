package provider_test

import (
	"testing"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
)

var passthroughImageIntentBenchmarkSink bool

func BenchmarkOpenAIPassthroughImageIntentReuse_LargeBody(b *testing.B) {
	body := buildLargeOpenAIResponsesImageToolBody(32 << 20)

	b.Run("Once", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			passthroughImageIntentBenchmarkSink = gatewayprovider.ImageIntent().IsImageGenerationIntent(media.OpenAIResponsesEndpoint, "gpt-5.4", body)
		}
	})

	b.Run("Twice", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			permissionIntent := gatewayprovider.ImageIntent().IsImageGenerationIntent(media.OpenAIResponsesEndpoint, "gpt-5.4", body)
			billingIntent := gatewayprovider.ImageIntent().IsImageGenerationIntent(media.OpenAIResponsesEndpoint, "gpt-5.4", body)
			passthroughImageIntentBenchmarkSink = permissionIntent && billingIntent
		}
	})
}
