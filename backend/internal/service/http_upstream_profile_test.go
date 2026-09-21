package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

func TestWithHTTPUpstreamProfile_DefaultKeepsContext(t *testing.T) {
	ctx := context.Background()
	got := upstream.WithHTTPUpstreamProfile(ctx, upstream.HTTPUpstreamProfileDefault)
	if got != ctx {
		t.Fatal("default profile should not wrap context")
	}
}

func TestWithHTTPUpstreamProfile_OpenAI(t *testing.T) {
	ctx := upstream.WithHTTPUpstreamProfile(context.TODO(), upstream.HTTPUpstreamProfileOpenAI)
	if profile := upstream.HTTPUpstreamProfileFromContext(ctx); profile != upstream.HTTPUpstreamProfileOpenAI {
		t.Fatalf("expected profile %q, got %q", upstream.HTTPUpstreamProfileOpenAI, profile)
	}
}

func TestWithHTTPUpstreamRedirectsDisabled(t *testing.T) {
	//nolint:staticcheck // 验证 nil context 的防御性回退逻辑。
	ctx := upstream.WithHTTPUpstreamRedirectsDisabled(nil)
	if !upstream.HTTPUpstreamRedirectsDisabled(ctx) {
		t.Fatal("expected redirects to be disabled")
	}
	if upstream.HTTPUpstreamRedirectsDisabled(context.Background()) {
		t.Fatal("redirects should remain enabled by default")
	}
}
