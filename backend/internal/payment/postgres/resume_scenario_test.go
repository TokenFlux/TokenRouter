package postgres_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"
)

// Payment resume links retain local order and merchant labels while signed
// tokens carry buyer, provider and return-context claims. URL fixtures
// separate destination canonicalization from later query decoration, including
// the decoration helper retaining a caller supplied query. Pure round trips
// inspect the claims actually returned by parsing.
//
// A configured signing key is required even when synthetic fallback-key tokens
// have a fallback signature. Environment-based migration fixtures run
// synchronously with automatic variable restoration and verify both new
// signing and legacy verification keys through testkit assembly. The synthetic
// helper signs the encoded JSON payload using the literal fixture key, allowing
// missing-key rejection to be exercised without giving production parsing a
// default signing key.

// TestNormalizePaymentSource compares empty source, WeChat alias and canonical
// source values in parallel cases. It expects the hosted default and the
// canonical WeChat resume label for the listed inputs.
func TestNormalizePaymentSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string // named source normalization case
		input  string // supplied source label text
		expect string // expected canonical source label
	}{
		{name: "empty uses default", input: "", expect: payment.PaymentSourceHostedRedirect},
		{name: "wechat alias normalized", input: "wechat_in_app", expect: payment.PaymentSourceWechatInAppResume},
		{name: "canonical value preserved", input: payment.PaymentSourceWechatInAppResume, expect: payment.PaymentSourceWechatInAppResume},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := payment.NormalizePaymentSource(tt.input); got != tt.expect {
				t.Fatalf("NormalizePaymentSource(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

// TestCanonicalizeReturnURL supplies an approved absolute result URL with
// query and fragment decoration. It expects canonicalization to return the
// fixed result destination with those extra components removed.
func TestCanonicalizeReturnURL(t *testing.T) {
	t.Parallel()

	got, err := payment.CanonicalizeReturnURL("https://example.com/payment/result?b=2#a", "example.com", "")
	if err != nil {
		t.Fatalf("CanonicalizeReturnURL returned error: %v", err)
	}
	if got != "https://example.com/payment/result" {
		t.Fatalf("CanonicalizeReturnURL = %q, want %q", got, "https://example.com/payment/result")
	}
}

// TestCanonicalizeReturnURLRejectsRelativeURL supplies a relative result path
// with an otherwise valid request host. It expects a validation error because
// the selected return destination requires an absolute URL.
func TestCanonicalizeReturnURLRejectsRelativeURL(t *testing.T) {
	t.Parallel()

	if _, err := payment.CanonicalizeReturnURL("/payment/result", "example.com", ""); err == nil {
		t.Fatal("CanonicalizeReturnURL should reject relative URLs")
	}
}

// TestCanonicalizeReturnURLRejectsExternalHost supplies a result URL on a host
// differing from the request host. It expects rejection when no approved
// frontend-host observation is supplied.
func TestCanonicalizeReturnURLRejectsExternalHost(t *testing.T) {
	t.Parallel()

	if _, err := payment.CanonicalizeReturnURL("https://evil.example/payment/result", "app.example.com", ""); err == nil {
		t.Fatal("CanonicalizeReturnURL should reject external hosts")
	}
}

// TestCanonicalizeReturnURLAllowsConfiguredFrontendHost supplies a frontend
// Referer host differing from the backend host. It expects that approved host
// to pass and its query decoration to be removed.
func TestCanonicalizeReturnURLAllowsConfiguredFrontendHost(t *testing.T) {
	t.Parallel()

	got, err := payment.CanonicalizeReturnURL(
		"https://app.example.com/payment/result?from=checkout",
		"api.example.com",
		"https://app.example.com/purchase",
	)
	if err != nil {
		t.Fatalf("CanonicalizeReturnURL returned error: %v", err)
	}
	if got != "https://app.example.com/payment/result" {
		t.Fatalf("CanonicalizeReturnURL = %q, want %q", got, "https://app.example.com/payment/result")
	}
}

// TestCanonicalizeReturnURLRejectsNonCanonicalPath supplies an approved host
// with an order-detail path. It expects rejection because return
// canonicalization permits the fixed payment result path for this input.
func TestCanonicalizeReturnURLRejectsNonCanonicalPath(t *testing.T) {
	t.Parallel()

	if _, err := payment.CanonicalizeReturnURL("https://app.example.com/orders/42", "app.example.com", ""); err == nil {
		t.Fatal("CanonicalizeReturnURL should reject non-canonical result paths")
	}
}

// TestBuildPaymentReturnURL supplies a base containing a query and fragment
// plus both order labels and resume token. It expects retained query, removed
// fragment and the order, merchant, token and success parameters.
func TestBuildPaymentReturnURL(t *testing.T) {
	t.Parallel()

	got, err := payment.ResumeBuildPaymentReturnURL("https://example.com/payment/result?from=checkout#fragment", 42, "sub2_42", "resume-token")
	if err != nil {
		t.Fatalf("buildPaymentReturnURL returned error: %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	if parsed.Fragment != "" {
		t.Fatalf("buildPaymentReturnURL should strip fragments, got %q", parsed.Fragment)
	}
	query := parsed.Query()
	if query.Get("from") != "checkout" {
		t.Fatalf("expected original query to be preserved, got %q", query.Get("from"))
	}
	if query.Get("order_id") != strconv.FormatInt(42, 10) {
		t.Fatalf("order_id = %q", query.Get("order_id"))
	}
	if query.Get("out_trade_no") != "sub2_42" {
		t.Fatalf("out_trade_no = %q", query.Get("out_trade_no"))
	}
	if query.Get("resume_token") != "resume-token" {
		t.Fatalf("resume_token = %q", query.Get("resume_token"))
	}
	if query.Get("status") != "success" {
		t.Fatalf("status = %q", query.Get("status"))
	}
}

// TestBuildPaymentReturnURLWithoutResumeTokenStillIncludesOutTradeNo omits the
// resume token while supplying numeric and merchant order labels. It expects
// both labels in the decorated return URL and no token value.
func TestBuildPaymentReturnURLWithoutResumeTokenStillIncludesOutTradeNo(t *testing.T) {
	t.Parallel()

	got, err := payment.ResumeBuildPaymentReturnURL("https://example.com/payment/result", 42, "sub2_42", "")
	if err != nil {
		t.Fatalf("buildPaymentReturnURL returned error: %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	query := parsed.Query()
	if query.Get("order_id") != "42" {
		t.Fatalf("order_id = %q", query.Get("order_id"))
	}
	if query.Get("out_trade_no") != "sub2_42" {
		t.Fatalf("out_trade_no = %q", query.Get("out_trade_no"))
	}
	if query.Get("resume_token") != "" {
		t.Fatalf("resume_token = %q, want empty", query.Get("resume_token"))
	}
}

// TestBuildPaymentReturnURLEmptyBase supplies an empty destination with
// populated order and token inputs. It expects an empty result and no error
// instead of constructing a relative return link.
func TestBuildPaymentReturnURLEmptyBase(t *testing.T) {
	t.Parallel()

	got, err := payment.ResumeBuildPaymentReturnURL("", 42, "sub2_42", "resume-token")
	if err != nil {
		t.Fatalf("buildPaymentReturnURL returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("buildPaymentReturnURL = %q, want empty string", got)
	}
}

// TestPaymentResumeTokenRoundTrip signs buyer, order, provider and return
// claims with a fixed key, leaving expiry to the creator default. It parses
// the token and asserts those supplied identity and return associations.
func TestPaymentResumeTokenRoundTrip(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateToken(payment.ResumeTokenClaims{
		OrderID:            42,
		UserID:             7,
		ProviderInstanceID: "19",
		ProviderKey:        "easypay",
		PaymentType:        "wxpay",
		CanonicalReturnURL: "https://example.com/payment/result",
		IssuedAt:           1234567890,
	})
	if err != nil {
		t.Fatalf("CreateToken returned error: %v", err)
	}

	claims, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken returned error: %v", err)
	}
	if claims.OrderID != 42 || claims.UserID != 7 {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if claims.ProviderInstanceID != "19" || claims.ProviderKey != "easypay" || claims.PaymentType != "wxpay" {
		t.Fatalf("claims provider snapshot mismatch: %+v", claims)
	}
	if claims.CanonicalReturnURL != "https://example.com/payment/result" {
		t.Fatalf("claims return URL = %q", claims.CanonicalReturnURL)
	}
}

// TestCreateTokenRejectsMissingSigningKey constructs the resume service with
// no primary signing key and requests an order token. It expects creation
// failure for that unavailable signing configuration.
func TestCreateTokenRejectsMissingSigningKey(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService(nil)
	_, err := svc.CreateToken(payment.ResumeTokenClaims{OrderID: 42})
	if err == nil {
		t.Fatal("CreateToken should reject missing signing key")
	}
}

// TestParseTokenRejectsFallbackSignedTokenWhenSigningKeyMissing creates a
// synthetic token using the literal fallback fixture key. A service lacking
// its primary signing key must reject that token instead of adopting the
// fixture key.
func TestParseTokenRejectsFallbackSignedTokenWhenSigningKeyMissing(t *testing.T) {
	t.Parallel()

	token := mustCreateFallbackSignedToken(t, payment.ResumeTokenClaims{OrderID: 42, UserID: 7})
	svc := payment.NewPaymentResumeService(nil)
	_, err := svc.ParseToken(token)
	if err == nil {
		t.Fatal("ParseToken should reject tokens when signing key is missing")
	}
}

// TestParseTokenRejectsExpiredToken signs an order token whose positive expiry
// is one hour in the past. It expects parsing failure after successful
// creation of that expired fixture.
func TestParseTokenRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateToken(payment.ResumeTokenClaims{
		OrderID:   42,
		UserID:    7,
		IssuedAt:  time.Now().Add(-25 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("CreateToken returned error: %v", err)
	}

	_, err = svc.ParseToken(token)
	if err == nil {
		t.Fatal("ParseToken should reject expired tokens")
	}
}

// TestWeChatPaymentResumeTokenRoundTrip signs OpenID, payment, subscription,
// redirect and scope claims with a fixed key. It parses the token and asserts
// those context fields while creation defaults the omitted expiry.
func TestWeChatPaymentResumeTokenRoundTrip(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		Amount:      "12.50",
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      7,
		RedirectTo:  "/purchase?from=wechat",
		Scope:       "snsapi_base",
		IssuedAt:    1234567890,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if claims.OpenID != "openid-123" || claims.PaymentType != payment.TypeWxpay {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if claims.Amount != "12.50" || claims.OrderType != payment.OrderTypeSubscription || claims.PlanID != 7 {
		t.Fatalf("claims payment context mismatch: %+v", claims)
	}
	if claims.RedirectTo != "/purchase?from=wechat" || claims.Scope != "snsapi_base" {
		t.Fatalf("claims redirect/scope mismatch: %+v", claims)
	}
}

// TestCreateWeChatPaymentResumeTokenRejectsMissingSigningKey constructs a
// service lacking a primary key and requests a WeChat token. It expects
// creation failure even though an OpenID is supplied.
func TestCreateWeChatPaymentResumeTokenRejectsMissingSigningKey(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService(nil)
	_, err := svc.CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{OpenID: "openid-123"})
	if err == nil {
		t.Fatal("CreateWeChatPaymentResumeToken should reject missing signing key")
	}
}

// TestParseWeChatPaymentResumeTokenRejectsFallbackSignedTokenWhenSigningKeyMissing
// supplies a synthetic correctly typed WeChat token signed with the literal
// fallback fixture key. It expects rejection by a service with no primary signing
// key.
func TestParseWeChatPaymentResumeTokenRejectsFallbackSignedTokenWhenSigningKeyMissing(t *testing.T) {
	t.Parallel()

	token := mustCreateFallbackSignedToken(t, payment.WeChatPaymentResumeClaims{
		TokenType:   payment.ResumeWechatPaymentResumeTokenType,
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
	})
	svc := payment.NewPaymentResumeService(nil)
	_, err := svc.ParseWeChatPaymentResumeToken(token)
	if err == nil {
		t.Fatal("ParseWeChatPaymentResumeToken should reject tokens when signing key is missing")
	}
}

// TestParseWeChatPaymentResumeTokenRejectsExpiredToken signs WeChat claims
// with a positive expiry one minute in the past. It expects parsing to reject
// those expired claims after token creation succeeds.
func TestParseWeChatPaymentResumeTokenRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		IssuedAt:    time.Now().Add(-30 * time.Minute).Unix(),
		ExpiresAt:   time.Now().Add(-1 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	_, err = svc.ParseWeChatPaymentResumeToken(token)
	if err == nil {
		t.Fatal("ParseWeChatPaymentResumeToken should reject expired tokens")
	}
}

// TestPaymentServiceParseWeChatPaymentResumeTokenUsesExplicitSigningKey sets
// the explicit environment key and creates a token directly with it. Testkit
// assembly given another legacy key must still parse that token and recover
// its OpenID.
func TestPaymentServiceParseWeChatPaymentResumeTokenUsesExplicitSigningKey(t *testing.T) {
	// Setenv registers restoration of this process setting; these configuration
	// cases remain synchronous while testkit reads the selected signing key.

	token, err := payment.NewPaymentResumeService([]byte("explicit-payment-resume-signing-key")).CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-explicit-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	svc := paymenttestkit.Resume("explicit-payment-resume-signing-key", []byte("0123456789abcdef0123456789abcdef"))

	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if claims.OpenID != "openid-explicit-key" {
		t.Fatalf("openid = %q, want %q", claims.OpenID, "openid-explicit-key")
	}
}

// TestPaymentServiceParseWeChatPaymentResumeTokenAcceptsLegacyEncryptionKeyDuringMigration
// configures an explicit key but signs a token with the legacy encryption key.
// It expects testkit configured parsing to accept that legacy signature and
// recover its OpenID.
func TestPaymentServiceParseWeChatPaymentResumeTokenAcceptsLegacyEncryptionKeyDuringMigration(t *testing.T) {
	// Setenv registers restoration of this process setting; these configuration
	// cases remain synchronous while testkit reads the selected signing key.

	legacyKey := []byte("0123456789abcdef0123456789abcdef")
	token, err := payment.NewPaymentResumeService(legacyKey).CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-legacy-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	svc := paymenttestkit.Resume("explicit-payment-resume-signing-key", legacyKey)

	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if claims.OpenID != "openid-legacy-key" {
		t.Fatalf("openid = %q, want %q", claims.OpenID, "openid-legacy-key")
	}
}

// TestNewConfiguredPaymentResumeServicePrefersExplicitSigningKeyAndKeepsLegacyVerificationFallback
// assembles explicit and legacy keys through testkit. It checks that generated
// tokens verify with the explicit key and legacy-signed tokens still parse
// through the assembled service.
func TestNewConfiguredPaymentResumeServicePrefersExplicitSigningKeyAndKeepsLegacyVerificationFallback(t *testing.T) {
	// Setenv registers restoration of this process setting; these configuration
	// cases remain synchronous while testkit reads the selected signing key.

	legacyKey := []byte("0123456789abcdef0123456789abcdef")
	svc := paymenttestkit.Resume("explicit-payment-resume-signing-key", legacyKey)

	explicitToken, err := svc.CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-explicit-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	explicitClaims, err := payment.NewPaymentResumeService([]byte("explicit-payment-resume-signing-key")).ParseWeChatPaymentResumeToken(explicitToken)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if explicitClaims.OpenID != "openid-explicit-key" {
		t.Fatalf("openid = %q, want %q", explicitClaims.OpenID, "openid-explicit-key")
	}

	legacyToken, err := payment.NewPaymentResumeService(legacyKey).CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-legacy-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	legacyClaims, err := svc.ParseWeChatPaymentResumeToken(legacyToken)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if legacyClaims.OpenID != "openid-legacy-key" {
		t.Fatalf("openid = %q, want %q", legacyClaims.OpenID, "openid-legacy-key")
	}
}

// mustCreateFallbackSignedToken builds a synthetic fallback-key token from
// arbitrary JSON-marshalable claims and fails the test on encoding error. It
// base64url encodes payload bytes without padding and computes an HMAC over
// that encoded text, then appends an encoded signature after a period.
//
// The literal key supplies the rejected fixture signature rather than
// configuring a production parser. Claims are serialized as supplied, so
// callers include any token discriminator or expiry their case requires. Hash
// Write errors are discarded because the fixed HMAC implementation accepts
// those bytes without an operational write failure.
func mustCreateFallbackSignedToken(t *testing.T, claims any) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte("tokenrouter-payment-resume"))
	// Sign encoded payload text exactly as the parser verifies it; the fixed HMAC
	// Write method cannot return an operational error.
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature
}
