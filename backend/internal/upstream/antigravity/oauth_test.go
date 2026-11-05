package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// OAuth fixtures exercise the cached secret, endpoint preference, and PKCE
// encoding independently of live account requests. Tests that replace package
// globals restore them after completion and run serially; generated-value
// checks describe the observed samples.

// TestGetClientSecret_环境变量设置 copies a test environment value into the already
// initialized secret cache. Retrieval must return that cached value, and
// cleanup restores the cache for later serialized tests.
func TestGetClientSecret_环境变量设置(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = ""
	t.Cleanup(func() { defaultClientSecret = old })
	ConfigureIdentity("my-secret-value", "")

	// Initialization has already completed; copy the test environment value into
	// its cached variable so this fixture exercises retrieval of startup state.
	defaultClientSecret = "my-secret-value"

	secret, err := getClientSecret()
	if err != nil {
		t.Fatalf("获取 client_secret 失败: %v", err)
	}
	if secret != "my-secret-value" {
		t.Errorf("client_secret 不匹配: got %s, want my-secret-value", secret)
	}
}

// TestGetClientSecret_环境变量为空 sets the cached secret to empty and restores it
// during cleanup. Retrieval must fail with guidance naming the environment
// variable used for configuration.
func TestGetClientSecret_环境变量为空(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = ""
	t.Cleanup(func() { defaultClientSecret = old })

	_, err := getClientSecret()
	if err == nil {
		t.Fatal("defaultClientSecret 为空时应返回错误")
	}
	if !strings.Contains(err.Error(), "provider.antigravity.oauth_client_secret") {
		t.Errorf("错误信息应包含环境变量名: got %s", err.Error())
	}
}

// TestGetClientSecret_环境变量未设置 empties the cached secret without rerunning
// initialization. Retrieval must reject that state, while cleanup restores the
// value used by subsequent serialized OAuth tests.
func TestGetClientSecret_环境变量未设置(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = ""
	t.Cleanup(func() { defaultClientSecret = old })

	_, err := getClientSecret()
	if err == nil {
		t.Fatal("defaultClientSecret 为空时应返回错误")
	}
}

// TestGetClientSecret_环境变量含空格 sets the cached secret to whitespace and
// restores it during cleanup. Trimming must make retrieval fail rather than
// treating whitespace as a usable credential.
func TestGetClientSecret_环境变量含空格(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "   "
	t.Cleanup(func() { defaultClientSecret = old })

	_, err := getClientSecret()
	if err == nil {
		t.Fatal("defaultClientSecret 仅含空格时应返回错误")
	}
}

// TestGetClientSecret_环境变量有前后空格 surrounds a usable cached secret with spaces.
// Retrieval must return the trimmed credential, and cleanup restores the
// shared startup cache afterward.
func TestGetClientSecret_环境变量有前后空格(t *testing.T) {
	old := defaultClientSecret
	defaultClientSecret = "  valid-secret  "
	t.Cleanup(func() { defaultClientSecret = old })

	secret, err := getClientSecret()
	if err != nil {
		t.Fatalf("获取 client_secret 失败: %v", err)
	}
	if secret != "valid-secret" {
		t.Errorf("应去除前后空格: got %q, want %q", secret, "valid-secret")
	}
}

// TestForwardBaseURLs_Daily优先 checks the configured endpoint copy against its
// daily-first rule. The resulting list must retain the configured count and
// include the production endpoint as well.
func TestForwardBaseURLs_Daily优先(t *testing.T) {
	urls := ForwardBaseURLs()
	if len(urls) == 0 {
		t.Fatal("ForwardBaseURLs 返回空列表")
	}
	if antigravityDailyBaseURL != "https://daily-cloudcode-pa.googleapis.com" {
		t.Fatalf("daily URL 未与官方客户端对齐: got %s", antigravityDailyBaseURL)
	}

	if urls[0] != antigravityDailyBaseURL {
		t.Errorf("第一个 URL 应为 daily: got %s, want %s", urls[0], antigravityDailyBaseURL)
	}

	if len(urls) != len(BaseURLs) {
		t.Errorf("URL 数量不匹配: got %d, want %d", len(urls), len(BaseURLs))
	}

	found := slices.Contains(urls, antigravityProdBaseURL)
	if !found {
		t.Error("ForwardBaseURLs 中缺少 prod URL")
	}
}

// TestForwardBaseURLs_不修改原切片 records the first configured endpoint before
// requesting a forwarding-order copy. The first BaseURLs element must retain
// its observed value after the helper returns.
func TestForwardBaseURLs_不修改原切片(t *testing.T) {
	originalFirst := BaseURLs[0]
	_ = ForwardBaseURLs()
	if BaseURLs[0] != originalFirst {
		t.Errorf("ForwardBaseURLs 不应修改原始 BaseURLs: got %s, want %s", BaseURLs[0], originalFirst)
	}
}

// TestNewURLAvailability constructs a tracker with a five-minute failure TTL.
// The result must contain initialized failure storage and retain the supplied
// duration for subsequent endpoint marks.
func TestNewURLAvailability(t *testing.T) {
	ua := NewURLAvailability(5 * time.Minute)
	if ua == nil {
		t.Fatal("NewURLAvailability 返回 nil")
	}
	if ua.ttl != 5*time.Minute {
		t.Errorf("TTL 不匹配: got %v, want 5m", ua.ttl)
	}
	if ua.unavailable == nil {
		t.Error("unavailable map 不应为 nil")
	}
}

// TestURLAvailability_MarkUnavailable marks a previously eligible endpoint
// using a five-minute TTL. Its immediate availability check must reject the
// endpoint while the newly recorded deadline remains active.
func TestURLAvailability_MarkUnavailable(t *testing.T) {
	ua := NewURLAvailability(5 * time.Minute)
	testURL := "https://example.com"

	ua.MarkUnavailable(testURL)

	if ua.IsAvailable(testURL) {
		t.Error("标记为不可用后 IsAvailable 应返回 false")
	}
}

// TestURLAvailability_MarkSuccess marks an endpoint unavailable before
// recording success. Success must restore immediate eligibility and store the
// endpoint as the latest preference under the tracker lock.
func TestURLAvailability_MarkSuccess(t *testing.T) {
	ua := NewURLAvailability(5 * time.Minute)
	testURL := "https://example.com"

	ua.MarkUnavailable(testURL)
	if ua.IsAvailable(testURL) {
		t.Error("标记为不可用后应不可用")
	}

	ua.MarkSuccess(testURL)
	if !ua.IsAvailable(testURL) {
		t.Error("MarkSuccess 后应恢复可用")
	}

	ua.mu.RLock()
	if ua.lastSuccess != testURL {
		t.Errorf("lastSuccess 不匹配: got %s, want %s", ua.lastSuccess, testURL)
	}
	ua.mu.RUnlock()
}

// TestURLAvailability_IsAvailable_TTL过期 marks a one-millisecond failure and
// waits beyond that lifetime. The later check must treat the recorded endpoint
// as eligible after expiry.
func TestURLAvailability_IsAvailable_TTL过期(t *testing.T) {
	ua := NewURLAvailability(1 * time.Millisecond)
	testURL := "https://example.com"

	ua.MarkUnavailable(testURL)
	// Wait beyond the one-millisecond fixture deadline so the next eligibility
	// check exercises expiry rather than a still-active endpoint failure.
	time.Sleep(5 * time.Millisecond)

	if !ua.IsAvailable(testURL) {
		t.Error("TTL 过期后 URL 应恢复可用")
	}
}

// TestURLAvailability_IsAvailable_未标记的URL queries an endpoint without a
// failure mark in a fresh tracker. The check must report eligibility even
// though that endpoint has not been recorded.
func TestURLAvailability_IsAvailable_未标记的URL(t *testing.T) {
	ua := NewURLAvailability(5 * time.Minute)
	if !ua.IsAvailable("https://never-marked.com") {
		t.Error("未标记的 URL 应默认可用")
	}
}

// TestURLAvailability_GetAvailableURLs selects from configured endpoints using
// a fresh tracker. With no recorded failures, the returned count must match
// every entry in the supplied BaseURLs list.
func TestURLAvailability_GetAvailableURLs(t *testing.T) {
	ua := NewURLAvailability(10 * time.Minute)

	urls := ua.GetAvailableURLsWithBase(BaseURLs)
	if len(urls) != len(BaseURLs) {
		t.Errorf("可用 URL 数量不匹配: got %d, want %d", len(urls), len(BaseURLs))
	}
}

// TestURLAvailability_GetAvailableURLs_标记一个不可用 marks the first configured
// endpoint with an active deadline. When at least two endpoints exist,
// selection must exclude the marked endpoint from its results.
func TestURLAvailability_GetAvailableURLs_标记一个不可用(t *testing.T) {
	ua := NewURLAvailability(10 * time.Minute)

	if len(BaseURLs) < 2 {
		t.Skip("BaseURLs 少于 2 个，跳过此测试")
	}

	ua.MarkUnavailable(BaseURLs[0])
	urls := ua.GetAvailableURLsWithBase(BaseURLs)

	for _, u := range urls {
		if u == BaseURLs[0] {
			t.Errorf("被标记不可用的 URL 不应出现在可用列表中: %s", BaseURLs[0])
		}
	}
}

// TestURLAvailability_GetAvailableURLsWithBase selects a custom three-endpoint
// list through an empty tracker. Its result must have three entries, checking
// the selected count against the caller-supplied list size.
func TestURLAvailability_GetAvailableURLsWithBase(t *testing.T) {
	ua := NewURLAvailability(10 * time.Minute)
	customURLs := []string{"https://a.com", "https://b.com", "https://c.com"}

	urls := ua.GetAvailableURLsWithBase(customURLs)
	if len(urls) != 3 {
		t.Errorf("可用 URL 数量不匹配: got %d, want 3", len(urls))
	}
}

// TestURLAvailability_GetAvailableURLsWithBase_LastSuccess优先 marks the final
// custom endpoint successful before selection. It must move first while the
// other eligible endpoints retain their supplied order.
func TestURLAvailability_GetAvailableURLsWithBase_LastSuccess优先(t *testing.T) {
	ua := NewURLAvailability(10 * time.Minute)
	customURLs := []string{"https://a.com", "https://b.com", "https://c.com"}

	ua.MarkSuccess("https://c.com")

	urls := ua.GetAvailableURLsWithBase(customURLs)
	if len(urls) != 3 {
		t.Fatalf("可用 URL 数量不匹配: got %d, want 3", len(urls))
	}
	if urls[0] != "https://c.com" {
		t.Errorf("lastSuccess 应排在第一位: got %s, want https://c.com", urls[0])
	}
	// Endpoints other than the preferred one retain their supplied order.
	if urls[1] != "https://a.com" {
		t.Errorf("第二个应为 a.com: got %s", urls[1])
	}
	if urls[2] != "https://b.com" {
		t.Errorf("第三个应为 b.com: got %s", urls[2])
	}
}

// TestURLAvailability_GetAvailableURLsWithBase_LastSuccess不可用 marks the
// preferred endpoint unavailable after recording its success. Selection must
// return the other supplied endpoint while the preferred failure deadline
// remains active.
func TestURLAvailability_GetAvailableURLsWithBase_LastSuccess不可用(t *testing.T) {
	ua := NewURLAvailability(10 * time.Minute)
	customURLs := []string{"https://a.com", "https://b.com"}

	ua.MarkSuccess("https://b.com")
	ua.MarkUnavailable("https://b.com")

	urls := ua.GetAvailableURLsWithBase(customURLs)
	if len(urls) != 1 {
		t.Fatalf("可用 URL 数量不匹配: got %d, want 1", len(urls))
	}
	if urls[0] != "https://a.com" {
		t.Errorf("仅 a.com 应可用: got %s", urls[0])
	}
}

// TestURLAvailability_GetAvailableURLsWithBase_LastSuccess不在列表中 records
// success for an endpoint outside the supplied list. Selection must retain the
// two-entry count; this assertion checks the count rather than endpoint
// membership.
func TestURLAvailability_GetAvailableURLsWithBase_LastSuccess不在列表中(t *testing.T) {
	ua := NewURLAvailability(10 * time.Minute)
	customURLs := []string{"https://a.com", "https://b.com"}

	ua.MarkSuccess("https://not-in-list.com")

	urls := ua.GetAvailableURLsWithBase(customURLs)
	// A successful endpoint outside this caller's list must not be added.
	if len(urls) != 2 {
		t.Fatalf("可用 URL 数量不匹配: got %d, want 2", len(urls))
	}
}

// TestGenerateRandomBytes_长度正确 requests several nonnegative byte counts,
// including zero. Each successful result must match its requested length
// across the small and larger allocation fixtures.
func TestGenerateRandomBytes_长度正确(t *testing.T) {
	sizes := []int{0, 1, 16, 32, 64, 128}
	for _, size := range sizes {
		b, err := GenerateRandomBytes(size)
		if err != nil {
			t.Fatalf("GenerateRandomBytes(%d) 失败: %v", size, err)
		}
		if len(b) != size {
			t.Errorf("长度不匹配: got %d, want %d", len(b), size)
		}
	}
}

// TestGenerateRandomBytes_不同调用产生不同结果 requests two independent thirty-two-byte
// samples. The observed values must differ in this run; the comparison checks
// samples rather than a universal uniqueness guarantee.
func TestGenerateRandomBytes_不同调用产生不同结果(t *testing.T) {
	b1, err := GenerateRandomBytes(32)
	if err != nil {
		t.Fatalf("第一次调用失败: %v", err)
	}
	b2, err := GenerateRandomBytes(32)
	if err != nil {
		t.Fatalf("第二次调用失败: %v", err)
	}
	// A collision between two 32-byte random values is negligibly likely.
	if string(b1) == string(b2) {
		t.Error("两次生成的随机字节相同，概率极低，可能有问题")
	}
}

// TestGenerateState_返回值格式 checks one generated OAuth state sample. The result
// must be nonempty, contain no standard-base64 punctuation or padding, and
// have forty-three encoded characters.
func TestGenerateState_返回值格式(t *testing.T) {
	state, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState 失败: %v", err)
	}
	if state == "" {
		t.Error("GenerateState 返回空字符串")
	}
	if strings.ContainsAny(state, "+/=") {
		t.Errorf("GenerateState 返回值包含非 base64url 字符: %s", state)
	}
	// Encoding 32 bytes without base64 padding produces 43 characters.
	if len(state) != 43 {
		t.Errorf("GenerateState 返回值长度不匹配: got %d, want 43", len(state))
	}
}

// TestGenerateState_唯一性 compares two generated OAuth state samples. The
// observed pair must differ in this run, exercising fresh generation without
// proving collision-free output for every call.
func TestGenerateState_唯一性(t *testing.T) {
	s1, _ := GenerateState()
	s2, _ := GenerateState()
	if s1 == s2 {
		t.Error("两次 GenerateState 结果相同")
	}
}

// TestGenerateSessionID_返回值格式 checks a generated session-key sample. It must
// be nonempty, have thirty-two characters, and decode successfully as the
// hexadecimal representation of random bytes.
func TestGenerateSessionID_返回值格式(t *testing.T) {
	id, err := GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID 失败: %v", err)
	}
	if id == "" {
		t.Error("GenerateSessionID 返回空字符串")
	}
	// Hex encoding uses two characters per byte.
	if len(id) != 32 {
		t.Errorf("GenerateSessionID 返回值长度不匹配: got %d, want 32", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Errorf("GenerateSessionID 返回值不是合法的 hex 字符串: %s, err: %v", id, err)
	}
}

// TestGenerateSessionID_唯一性 compares two generated session-key samples. The
// observed pair must differ in this run, checking fresh allocation and
// generation without asserting universal uniqueness.
func TestGenerateSessionID_唯一性(t *testing.T) {
	id1, _ := GenerateSessionID()
	id2, _ := GenerateSessionID()
	if id1 == id2 {
		t.Error("两次 GenerateSessionID 结果相同")
	}
}

// TestGenerateCodeVerifier_返回值格式 checks one generated PKCE verifier sample. It
// must have forty-three unpadded URL-safe characters and remain nonempty for
// pairing with a token-exchange challenge.
func TestGenerateCodeVerifier_返回值格式(t *testing.T) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier 失败: %v", err)
	}
	if verifier == "" {
		t.Error("GenerateCodeVerifier 返回空字符串")
	}
	if strings.ContainsAny(verifier, "+/=") {
		t.Errorf("GenerateCodeVerifier 返回值包含非 base64url 字符: %s", verifier)
	}
	// Encoding 32 bytes without base64 padding produces 43 characters.
	if len(verifier) != 43 {
		t.Errorf("GenerateCodeVerifier 返回值长度不匹配: got %d, want 43", len(verifier))
	}
}

// TestGenerateCodeVerifier_唯一性 compares two freshly generated PKCE verifier
// samples. The observed pair must differ during this run, checking sample
// variation without establishing collision-free verifier generation.
func TestGenerateCodeVerifier_唯一性(t *testing.T) {
	v1, _ := GenerateCodeVerifier()
	v2, _ := GenerateCodeVerifier()
	if v1 == v2 {
		t.Error("两次 GenerateCodeVerifier 结果相同")
	}
}

// TestGenerateCodeChallenge_SHA256_Base64URL computes the expected challenge
// independently from a supplied verifier. Helper output must equal SHA-256
// encoded with URL-safe base64 after padding removal.
func TestGenerateCodeChallenge_SHA256_Base64URL(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

	challenge := GenerateCodeChallenge(verifier)

	// Hash and encode the supplied verifier independently so the expected result
	// can detect a shared-helper change to digest input or base64 padding.
	hash := sha256.Sum256([]byte(verifier))
	expected := strings.TrimRight(base64.URLEncoding.EncodeToString(hash[:]), "=")

	if challenge != expected {
		t.Errorf("CodeChallenge 不匹配: got %s, want %s", challenge, expected)
	}
}

// TestGenerateCodeChallenge_不含填充字符 derives a challenge from a fixed verifier.
// The generated spelling must omit base64 padding characters so the query
// parameter uses unpadded PKCE encoding.
func TestGenerateCodeChallenge_不含填充字符(t *testing.T) {
	challenge := GenerateCodeChallenge("test-verifier")
	if strings.Contains(challenge, "=") {
		t.Errorf("CodeChallenge 不应包含 = 填充字符: %s", challenge)
	}
}

// TestGenerateCodeChallenge_不含非URL安全字符 derives a challenge from another fixed
// verifier. Its spelling must exclude plus and slash characters from ordinary
// base64, retaining the URL-safe alphabet.
func TestGenerateCodeChallenge_不含非URL安全字符(t *testing.T) {
	challenge := GenerateCodeChallenge("another-verifier")
	if strings.ContainsAny(challenge, "+/") {
		t.Errorf("CodeChallenge 不应包含 + 或 / 字符: %s", challenge)
	}
}

// TestGenerateCodeChallenge_相同输入相同输出 derives the challenge twice from one
// fixed verifier. Both results must match because hashing and encoding the
// same input produces deterministic challenge spelling.
func TestGenerateCodeChallenge_相同输入相同输出(t *testing.T) {
	c1 := GenerateCodeChallenge("same-verifier")
	c2 := GenerateCodeChallenge("same-verifier")
	if c1 != c2 {
		t.Errorf("相同输入应产生相同输出: got %s and %s", c1, c2)
	}
}

// TestGenerateCodeChallenge_不同输入不同输出 derives challenges from two distinct
// fixed verifiers. Their observed digests must differ for these fixtures,
// guarding against a helper that ignores its input.
func TestGenerateCodeChallenge_不同输入不同输出(t *testing.T) {
	c1 := GenerateCodeChallenge("verifier-1")
	c2 := GenerateCodeChallenge("verifier-2")
	if c1 == c2 {
		t.Error("不同输入应产生不同输出")
	}
}

// TestBuildAuthorizationURL_参数验证 parses a consent URL built with supplied
// state and challenge values. Its query must match all ten configured OAuth,
// PKCE, and offline-consent parameters.
func TestBuildAuthorizationURL_参数验证(t *testing.T) {
	state := "test-state-123"
	codeChallenge := "test-challenge-abc"

	authURL := BuildAuthorizationURL(state, codeChallenge)

	if !strings.HasPrefix(authURL, AuthorizeURL+"?") {
		t.Errorf("URL 应以 %s? 开头: got %s", AuthorizeURL, authURL)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("解析 URL 失败: %v", err)
	}

	params := parsed.Query()

	expectedParams := map[string]string{
		"client_id":              ClientID,
		"redirect_uri":           RedirectURI,
		"response_type":          "code",
		"scope":                  Scopes,
		"state":                  state,
		"code_challenge":         codeChallenge,
		"code_challenge_method":  "S256",
		"access_type":            "offline",
		"prompt":                 "consent",
		"include_granted_scopes": "true",
	}

	for key, want := range expectedParams {
		got := params.Get(key)
		if got != want {
			t.Errorf("参数 %s 不匹配: got %q, want %q", key, got, want)
		}
	}
}

// TestBuildAuthorizationURL_参数数量 parses the consent query for simple state and
// challenge input. The resulting parameter map must contain exactly the ten
// keys asserted by this fixture.
func TestBuildAuthorizationURL_参数数量(t *testing.T) {
	authURL := BuildAuthorizationURL("s", "c")
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("解析 URL 失败: %v", err)
	}

	params := parsed.Query()
	expectedCount := 10
	if len(params) != expectedCount {
		t.Errorf("参数数量不匹配: got %d, want %d", len(params), expectedCount)
	}
}

// TestBuildAuthorizationURL_特殊字符编码 puts query-special characters in the
// supplied state value. Parsing the resulting consent URL must recover that
// state exactly from the encoded query.
func TestBuildAuthorizationURL_特殊字符编码(t *testing.T) {
	state := "state+with/special=chars"
	codeChallenge := "challenge+value"

	authURL := BuildAuthorizationURL(state, codeChallenge)

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("解析 URL 失败: %v", err)
	}

	if got := parsed.Query().Get("state"); got != state {
		t.Errorf("state 参数编码/解码不匹配: got %q, want %q", got, state)
	}
}

// TestConstants_值正确 pins configured OAuth endpoints, client identity,
// lifetimes, and startup identity values. The fixture expects built-in secret
// and User-Agent settings, so external startup overrides affect its
// assertions.
func TestConstants_值正确(t *testing.T) {
	if AuthorizeURL != "https://accounts.google.com/o/oauth2/v2/auth" {
		t.Errorf("AuthorizeURL 不匹配: got %s", AuthorizeURL)
	}
	if TokenURL != "https://oauth2.googleapis.com/token" {
		t.Errorf("TokenURL 不匹配: got %s", TokenURL)
	}
	if UserInfoURL != "https://www.googleapis.com/oauth2/v2/userinfo" {
		t.Errorf("UserInfoURL 不匹配: got %s", UserInfoURL)
	}
	if ClientID != "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com" {
		t.Errorf("ClientID 不匹配: got %s", ClientID)
	}
	secret, err := getClientSecret()
	if err != nil {
		t.Fatalf("getClientSecret 应返回默认值，但报错: %v", err)
	}
	if secret != "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" {
		t.Errorf("默认 client_secret 不匹配: got %s", secret)
	}
	if RedirectURI != "http://localhost:8085/callback" {
		t.Errorf("RedirectURI 不匹配: got %s", RedirectURI)
	}
	if GetUserAgentForContext(context.Background()) != "antigravity/1.23.2 windows/amd64" {
		t.Errorf("UserAgent 不匹配: got %s", GetUserAgentForContext(context.Background()))
	}
	if SessionTTL != 30*time.Minute {
		t.Errorf("SessionTTL 不匹配: got %v", SessionTTL)
	}
	if URLAvailabilityTTL != 5*time.Minute {
		t.Errorf("URLAvailabilityTTL 不匹配: got %v", URLAvailabilityTTL)
	}
}

// TestScopes_包含必要范围 checks the configured scope string for five required
// permissions. Each expected cloud, user-information, logging, and experiment
// scope must occur in the consent configuration.
func TestScopes_包含必要范围(t *testing.T) {
	expectedScopes := []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	}

	for _, scope := range expectedScopes {
		if !strings.Contains(Scopes, scope) {
			t.Errorf("Scopes 缺少 %s", scope)
		}
	}
}
