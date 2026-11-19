package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// Gemini native admission carries client model, signature and conversation-
// digest state into the shared executor. These scenarios compare sticky and
// digest recovery alongside CLI path identity, checking that moderation sees
// the request before mapped execution begins.

// TestExtractGeminiCLISessionHash supplies directory patterns with and without
// privileged-user identity. It checks the combined SHA-256 value, directory-
// only result and empty result when no directory pattern is present.
func TestExtractGeminiCLISessionHash(t *testing.T) {
	tests := []struct {
		name             string // scenario subtest label
		body             string // raw native request
		privilegedUserID string // optional identity header
		wantEmpty        bool   // expected missing identity
		wantHash         string // expected captured digest
	}{
		{
			name:             "with privileged-user-id and tmp dir",
			body:             `{"contents":[{"parts":[{"text":"The project's temporary directory is: /Users/ianshaw/.gemini/tmp/f7851b009ed314d1baee62e83115f486160283f4a55a582d89fdac8b9fe3b740"}]}]}`,
			privilegedUserID: "90785f52-8bbe-4b17-b111-a1ddea1636c3",
			wantEmpty:        false,
			wantHash: func() string {
				combined := "90785f52-8bbe-4b17-b111-a1ddea1636c3:f7851b009ed314d1baee62e83115f486160283f4a55a582d89fdac8b9fe3b740"
				hash := sha256.Sum256([]byte(combined))
				return hex.EncodeToString(hash[:])
			}(),
		},
		{
			name:             "without privileged-user-id but with tmp dir",
			body:             `{"contents":[{"parts":[{"text":"The project's temporary directory is: /Users/ianshaw/.gemini/tmp/f7851b009ed314d1baee62e83115f486160283f4a55a582d89fdac8b9fe3b740"}]}]}`,
			privilegedUserID: "",
			wantEmpty:        false,
			wantHash:         "f7851b009ed314d1baee62e83115f486160283f4a55a582d89fdac8b9fe3b740",
		},
		{
			name:             "without tmp dir",
			body:             `{"contents":[{"parts":[{"text":"Hello world"}]}]}`,
			privilegedUserID: "90785f52-8bbe-4b17-b111-a1ddea1636c3",
			wantEmpty:        true,
		},
		{
			name:             "empty body",
			body:             "",
			privilegedUserID: "90785f52-8bbe-4b17-b111-a1ddea1636c3",
			wantEmpty:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
			if tt.privilegedUserID != "" {
				c.Request.Header.Set("x-gemini-api-privileged-user-id", tt.privilegedUserID)
			}

			result := ExtractGeminiCLISessionHash(c, []byte(tt.body))

			if tt.wantEmpty {
				require.Empty(t, result, "expected empty session hash")
			} else {
				require.NotEmpty(t, result, "expected non-empty session hash")
				require.Equal(t, tt.wantHash, result, "session hash mismatch")
			}
		})
	}
}

// TestGeminiCLITmpDirRegex presents valid CLI paths, surrounding text and an
// insufficient digest. It checks the matched capture and rejects the short
// hash and unrelated text fixtures.
func TestGeminiCLITmpDirRegex(t *testing.T) {
	tests := []struct {
		name      string // scenario subtest label
		input     string // input fixture text
		wantMatch bool   // expected regex match
		wantHash  string // expected captured digest
	}{
		{
			name:      "valid tmp dir path",
			input:     "/Users/ianshaw/.gemini/tmp/f7851b009ed314d1baee62e83115f486160283f4a55a582d89fdac8b9fe3b740",
			wantMatch: true,
			wantHash:  "f7851b009ed314d1baee62e83115f486160283f4a55a582d89fdac8b9fe3b740",
		},
		{
			name:      "valid tmp dir path in text",
			input:     "The project's temporary directory is: /Users/ianshaw/.gemini/tmp/f7851b009ed314d1baee62e83115f486160283f4a55a582d89fdac8b9fe3b740\nOther text",
			wantMatch: true,
			wantHash:  "f7851b009ed314d1baee62e83115f486160283f4a55a582d89fdac8b9fe3b740",
		},
		{
			name:      "invalid hash length",
			input:     "/Users/ianshaw/.gemini/tmp/abc123",
			wantMatch: false,
		},
		{
			name:      "no tmp dir",
			input:     "Hello world",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := GeminiCLITmpDirRegex.FindStringSubmatch(tt.input)
			if tt.wantMatch {
				require.NotNil(t, match, "expected regex to match")
				require.Len(t, match, 2, "expected 2 capture groups")
				require.Equal(t, tt.wantHash, match[1], "hash mismatch")
			} else {
				require.Nil(t, match, "expected regex not to match")
			}
		})
	}
}

// TestSafeShortPrefix checks empty, short, exact and longer values against a
// byte-prefix length. It protects unchanged output for a nonpositive limit and
// the requested prefix for longer strings.
func TestSafeShortPrefix(t *testing.T) {
	tests := []struct {
		name  string // scenario subtest label
		input string // input fixture text
		n     int    // maximum prefix bytes
		want  string // expected prefix text
	}{
		{name: "空字符串", input: "", n: 8, want: ""},
		{name: "长度小于截断值", input: "abc", n: 8, want: "abc"},
		{name: "长度等于截断值", input: "12345678", n: 8, want: "12345678"},
		{name: "长度大于截断值", input: "1234567890", n: 8, want: "12345678"},
		{name: "截断值为0", input: "123456", n: 0, want: "123456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, GeminiShortPrefix(tt.input, tt.n))
		})
	}
}

// TestGeminiNativePrefacePreservesModerationBeforeMapping blocks moderation on
// an intentionally invalid body. It checks request observation and prompt
// replacement ordering, then confirms the blocked response ends preparation
// before execution.
func TestGeminiNativePrefacePreservesModerationBeforeMapping(t *testing.T) {
	base := &prefaceBackend{key: prefaceKey(), block: true}
	p := &geminiPrefaceBackend{base: base}
	h := NewGeminiNativeHandler(GeminiNativeOptions{}, p, base, nil, nil, p)
	c, w := prefaceContext(`not-json`)
	c.Params = gin.Params{{Key: "modelAction", Value: "/m:generateContent"}}
	h.GeminiV1BetaModels(c)
	require.Equal(t, []string{"request:m", "endpoint", "prompt:gemini", "moderate:gemini"}, base.events)
	require.Contains(t, w.Body.String(), "blocked")
}

// TestGeminiNativePrefaceCarriesSignatureAndDigestState exercises native and
// streaming actions with sticky and digest-recovered providers. It checks
// original and mapped models, signature hints, funding pointers and native
// concurrency output passed to execution.
func TestGeminiNativePrefaceCarriesSignatureAndDigestState(t *testing.T) {
	for _, action := range []string{"generateContent", "streamGenerateContent"} {
		for _, bound := range []int64{65, 0} {
			t.Run(action+"/"+map[bool]string{true: "sticky", false: "digest"}[bound > 0], func(t *testing.T) {
				base := &prefaceBackend{key: prefaceKey()}
				p := &geminiPrefaceBackend{base: base, bound: bound}
				concurrency := prefaceConcurrency()
				h := NewGeminiNativeHandler(GeminiNativeOptions{}, p, base, concurrency, func() string { t.Fatal("matched session must not create id"); return "" }, p)
				body := `{"contents":[{"role":"user","parts":[{"text":"/.gemini/tmp/` + strings.Repeat("a", 64) + `"}]}]}`
				c, w := prefaceContext(body)
				c.Params = gin.Params{{Key: "modelAction", Value: "/original:" + action}}
				subscription := &billing.UserSubscription{ID: 12}
				c.Set(ContextKeySubscription, subscription)
				c.Request = c.Request.WithContext(requeststate.WithClientProtocol(c.Request.Context(), protocol.ProtocolGeminiGenerateContent))

				h.GeminiV1BetaModels(c)

				require.Equal(t, 200, w.Code, w.Body.String())
				plan := routing.Plan(routing.PlanInput{RequestedModel: "original", GroupID: base.key.GroupID, GroupMapping: routing.GroupMappingResult{Mapped: true, MappedModel: "mapped-model"}})
				wantText := execution.TextState{
					Kind:            execution.TextNativeGemini,
					BoundProviderID: bound,
					HasBoundSession: true,
					GeminiModel:     "mapped-model",
					Action:          action,
					SignatureState:  requeststate.GeminiSignatureState{BoundProviderID: bound},
					Mapping:         plan.Mapping(),
				}
				var wantHints requeststate.ExecutionHints
				wantEvents := []string{"request:original", "endpoint", "prompt:gemini", "moderate:gemini", "plan", "bind", "errors", "eligibility"}
				if bound == 0 {
					wantText.BoundProviderID = 87
					wantText.SignatureState.BoundProviderID = 87
					wantText.UseDigestFallback = true
					wantText.DigestChain = "digest"
					wantText.PrefixHash = "prefix"
					wantText.SessionUUID = "session-id"
					wantText.MatchedDigestChain = "previous"
					wantEvents = append(wantEvents, "sticky")
				} else {
					wantHints.PrefetchedStickyProviderID = requeststate.Hint[int64]{Value: bound, Set: true}
					wantHints.PrefetchedStickyGroupID = requeststate.Hint[int64]{Value: *base.key.GroupID, Set: true}
					wantEvents = append(wantEvents, "prefetch")
				}
				wantEvents = append(wantEvents, "execution")
				wantRouting := requeststate.WithRoutePlan(requeststate.WithClientProtocol(c.Request.Context(), protocol.ProtocolGeminiGenerateContent), plan)
				require.Equal(t, execution.Request{
					Hints:       wantHints,
					Routing:     requeststate.RoutingStateFromContext(wantRouting),
					Route:       plan,
					UserID:      42,
					Stream:      action == "streamGenerateContent",
					Body:        []byte(body),
					Model:       "original",
					Funding:     execution.FundingState{Key: base.key, Subscription: subscription},
					SessionHash: "gemini:" + strings.Repeat("a", 64),
					Text:        wantText,
				}, p.request)
				require.Same(t, base.key, p.request.Funding.Key)
				require.Same(t, subscription, p.request.Funding.Subscription)
				require.Equal(t, wantEvents, base.events)
				require.NotNil(t, p.output)
				require.Same(t, c, p.output.HTTP)
				require.Same(t, c.Writer, p.output.Writer)
				require.NotNil(t, p.output.Log)
				require.NotNil(t, p.output.StreamStarted)
				require.False(t, *p.output.StreamStarted)
				require.NotNil(t, p.output.Concurrency)
				require.NotSame(t, concurrency, p.output.Concurrency)
				require.Same(t, concurrency.Service(), p.output.Concurrency.Service())
			})
		}
	}
}

// TestGeminiNativeRejectsDuplicateAndCaseVariantJSONBeforeModeration submits
// duplicate contents and lowercase systeminstruction variants to native Gemini
// generation. Validation must return HTTP 400 before prompt replacement,
// moderation, or route planning runs.
func TestGeminiNativeRejectsDuplicateAndCaseVariantJSONBeforeModeration(t *testing.T) {
	for _, body := range []string{
		`{"contents":[{"role":"user","parts":[{"text":"safe"}]}],"contents":[{"role":"user","parts":[{"text":"unsafe"}]}]}`,
		`{"systeminstruction":{"parts":[{"text":"override"}]},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
	} {
		base := &prefaceBackend{key: prefaceKey()}
		p := &geminiPrefaceBackend{base: base}
		h := NewGeminiNativeHandler(GeminiNativeOptions{}, p, base, nil, nil, p)
		c, w := prefaceContext(body)
		c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-2.5-pro:generateContent"}}
		h.GeminiV1BetaModels(c)
		require.Equal(t, 400, w.Code, body)
		require.NotContains(t, base.events, "moderate:gemini")
		require.NotContains(t, base.events, "plan")
	}
}
