package codeassist

import (
	"encoding/hex"
	"strings"
	"testing"
)

// TestGenerateRandomBytes requests zero and several positive byte lengths from
// the shared generator. It checks every successful sample has the requested
// length and reports a returned error for the corresponding input.
func TestGenerateRandomBytes(t *testing.T) {
	tests := []int{0, 1, 16, 32, 64}
	for _, n := range tests {
		b, err := GenerateRandomBytes(n)
		if err != nil {
			t.Errorf("GenerateRandomBytes(%d) 出错: %v", n, err)
			continue
		}
		if len(b) != n {
			t.Errorf("GenerateRandomBytes(%d) 返回长度=%d，期望=%d", n, len(b), n)
		}
	}
}

// TestGenerateRandomBytes_Uniqueness takes two independent thirty-two-byte
// samples. It checks their bytes differ in this run, protecting the generator
// wiring without establishing a universal collision-free guarantee.
func TestGenerateRandomBytes_Uniqueness(t *testing.T) {
	// A collision between independent 32-byte samples is negligibly likely.
	a, _ := GenerateRandomBytes(32)
	b, _ := GenerateRandomBytes(32)
	if string(a) == string(b) {
		t.Error("两次 GenerateRandomBytes(32) 返回了相同结果，随机性可能有问题")
	}
}

// TestGenerateState obtains one authorization state value. It checks the value
// is nonempty and its unpadded URL-safe spelling excludes equals, plus, and
// slash characters.
func TestGenerateState(t *testing.T) {
	state, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState() 出错: %v", err)
	}
	if state == "" {
		t.Error("GenerateState() 返回空字符串")
	}
	if strings.Contains(state, "=") {
		t.Errorf("GenerateState() 结果包含 '=' padding: %s", state)
	}
	if strings.ContainsAny(state, "+/") {
		t.Errorf("GenerateState() 结果包含非 base64url 字符: %s", state)
	}
}

// TestGenerateSessionID obtains one local authorization session identifier. It
// checks the thirty-two-character output decodes as hexadecimal, protecting
// the sixteen-byte representation expected by session storage.
func TestGenerateSessionID(t *testing.T) {
	sid, err := GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID() 出错: %v", err)
	}
	// Sixteen random bytes produce 32 hexadecimal characters.
	if len(sid) != 32 {
		t.Errorf("GenerateSessionID() 长度=%d，期望=32", len(sid))
	}
	if _, err := hex.DecodeString(sid); err != nil {
		t.Errorf("GenerateSessionID() 不是合法的 hex 字符串: %s, err=%v", sid, err)
	}
}

// TestGenerateSessionID_Uniqueness generates two session identifiers in one
// run. It checks their values differ, protecting independent sampling without
// claiming collisions are impossible for every future sample.
func TestGenerateSessionID_Uniqueness(t *testing.T) {
	a, _ := GenerateSessionID()
	b, _ := GenerateSessionID()
	if a == b {
		t.Error("两次 GenerateSessionID() 返回了相同结果")
	}
}

// TestGenerateCodeVerifier obtains a shared PKCE verifier. It checks a
// nonempty value, the minimum forty-three-character length, and an unpadded
// URL-safe alphabet suitable for the authorization request.
func TestGenerateCodeVerifier(t *testing.T) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier() 出错: %v", err)
	}
	if verifier == "" {
		t.Error("GenerateCodeVerifier() 返回空字符串")
	}
	// RFC 7636 requires a verifier of at least 43 characters.
	if len(verifier) < 43 {
		t.Errorf("GenerateCodeVerifier() 长度=%d，RFC 7636 要求至少 43 字符", len(verifier))
	}
	if strings.Contains(verifier, "=") {
		t.Errorf("GenerateCodeVerifier() 包含 '=' padding: %s", verifier)
	}
	if strings.ContainsAny(verifier, "+/") {
		t.Errorf("GenerateCodeVerifier() 包含非 base64url 字符: %s", verifier)
	}
}

// TestGenerateCodeChallenge uses the published Appendix B verifier/challenge
// pair stored in the fixture. It checks the exact S256 output so hashing and
// base64url encoding remain compatible.
func TestGenerateCodeChallenge(t *testing.T) {
	// Use the verifier and challenge from RFC 7636 Appendix B.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	expected := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	challenge := GenerateCodeChallenge(verifier)
	if challenge != expected {
		t.Errorf("GenerateCodeChallenge(%q) = %q，期望 %q", verifier, challenge, expected)
	}
}

// TestGenerateCodeChallenge_NoPadding hashes a fixed verifier string. It
// checks the challenge contains no equals padding, preserving the wire
// spelling used for the consent URL parameter.
func TestGenerateCodeChallenge_NoPadding(t *testing.T) {
	challenge := GenerateCodeChallenge("test-verifier-string")
	if strings.Contains(challenge, "=") {
		t.Errorf("GenerateCodeChallenge() 结果包含 '=' padding: %s", challenge)
	}
}

// TestBase64URLEncode encodes empty, single-byte, multi-byte, and all-zero
// inputs. It checks every output avoids equals padding and the plus/slash
// symbols of ordinary base64.
func TestBase64URLEncode(t *testing.T) {
	tests := []struct {
		name  string // case identity label
		input []byte // source encoding byte sequence
	}{
		{"空字节", []byte{}},
		{"单字节", []byte{0xff}},
		{"多字节", []byte{0x01, 0x02, 0x03, 0x04, 0x05}},
		{"全零", []byte{0x00, 0x00, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := base64URLEncode(tt.input)
			if strings.Contains(result, "=") {
				t.Errorf("base64URLEncode(%v) 包含 '=' padding: %s", tt.input, result)
			}
			if strings.ContainsAny(result, "+/") {
				t.Errorf("base64URLEncode(%v) 包含非 URL 安全字符: %s", tt.input, result)
			}
		})
	}
}

// TestHasRestrictedScope compares Generative Language and Drive prefixes with
// ordinary cloud/profile and unrelated values. It checks the local
// built-in-client filter recognizes the configured prefix families and leaves
// the other fixture values unrestricted.
func TestHasRestrictedScope(t *testing.T) {
	tests := []struct {
		scope    string // scope text under classification
		expected bool   // expected restricted-prefix classification
	}{
		// Restricted scope prefixes.
		{"https://www.googleapis.com/auth/generative-language", true},
		{"https://www.googleapis.com/auth/generative-language.retriever", true},
		{"https://www.googleapis.com/auth/generative-language.tuning", true},
		{"https://www.googleapis.com/auth/drive", true},
		{"https://www.googleapis.com/auth/drive.readonly", true},
		{"https://www.googleapis.com/auth/drive.file", true},
		// Scope tokens outside the local restricted prefixes.
		{"https://www.googleapis.com/auth/cloud-platform", false},
		{"https://www.googleapis.com/auth/userinfo.email", false},
		{"https://www.googleapis.com/auth/userinfo.profile", false},
		// Empty and unrelated values are unrestricted.
		{"", false},
		{"random-scope", false},
	}
	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			got := hasRestrictedScope(tt.scope)
			if got != tt.expected {
				t.Errorf("hasRestrictedScope(%q) = %v，期望 %v", tt.scope, got, tt.expected)
			}
		})
	}
}

// TestBuildAuthorizationURL builds a Code Assist consent URL using the
// built-in identity. It checks required PKCE/session/consent parameters, the
// Google endpoint prefix, and omission of an absent project identifier.
func TestBuildAuthorizationURL(t *testing.T) {
	authURL, err := BuildAuthorizationURL(
		OAuthConfig{},
		"test-state",
		"test-challenge",
		"https://example.com/callback",
		"",
		"code_assist",
	)
	if err != nil {
		t.Fatalf("BuildAuthorizationURL() 出错: %v", err)
	}

	checks := []string{
		"response_type=code",
		"client_id=" + GeminiCLIOAuthClientID,
		"redirect_uri=",
		"state=test-state",
		"code_challenge=test-challenge",
		"code_challenge_method=S256",
		"access_type=offline",
		"prompt=consent",
		"include_granted_scopes=true",
	}
	for _, check := range checks {
		if !strings.Contains(authURL, check) {
			t.Errorf("BuildAuthorizationURL() URL 缺少参数 %q\nURL: %s", check, authURL)
		}
	}

	// An empty project ID omits the optional project_id parameter.
	if strings.Contains(authURL, "project_id=") {
		t.Errorf("BuildAuthorizationURL() 空 projectID 时不应包含 project_id 参数")
	}

	if !strings.HasPrefix(authURL, AuthorizeURL+"?") {
		t.Errorf("BuildAuthorizationURL() URL 应以 %s? 开头，实际: %s", AuthorizeURL, authURL)
	}
}

// TestBuildAuthorizationURL_EmptyRedirectURI supplies an empty callback to
// built-in Code Assist authorization. It checks the builder returns a
// redirect_uri error before producing a usable consent URL.
func TestBuildAuthorizationURL_EmptyRedirectURI(t *testing.T) {
	_, err := BuildAuthorizationURL(
		OAuthConfig{},
		"test-state",
		"test-challenge",
		"", // Empty redirect URI.
		"",
		"code_assist",
	)
	if err == nil {
		t.Error("BuildAuthorizationURL() 空 redirectURI 应该报错")
	}
	if !strings.Contains(err.Error(), "redirect_uri") {
		t.Errorf("错误消息应包含 'redirect_uri'，实际: %v", err)
	}
}

// TestBuildAuthorizationURL_WithProjectID builds authorization with a
// configured project identifier. It checks the optional project_id parameter
// appears alongside the ordinary consent request so project selection reaches
// the browser flow.
func TestBuildAuthorizationURL_WithProjectID(t *testing.T) {
	authURL, err := BuildAuthorizationURL(
		OAuthConfig{},
		"test-state",
		"test-challenge",
		"https://example.com/callback",
		"my-project-123",
		"code_assist",
	)
	if err != nil {
		t.Fatalf("BuildAuthorizationURL() 出错: %v", err)
	}
	if !strings.Contains(authURL, "project_id=my-project-123") {
		t.Errorf("BuildAuthorizationURL() 带 projectID 时应包含 project_id 参数\nURL: %s", authURL)
	}
}

// TestBuildAuthorizationURL_UsesBuiltinSecretFallback clears the secret
// environment value and leaves client configuration empty. It checks consent
// URL construction still selects the built-in client ID using its configured
// constant.
func TestBuildAuthorizationURL_UsesBuiltinSecretFallback(t *testing.T) {
	authURL, err := BuildAuthorizationURL(
		OAuthConfig{},
		"test-state",
		"test-challenge",
		"https://example.com/callback",
		"",
		"code_assist",
	)
	if err != nil {
		t.Fatalf("BuildAuthorizationURL() 不应报错: %v", err)
	}
	if !strings.Contains(authURL, "client_id="+GeminiCLIOAuthClientID) {
		t.Errorf("应使用内置 Gemini CLI client_id，实际 URL: %s", authURL)
	}
}

// TestEffectiveOAuthConfig_GoogleOne compares built-in and custom credential
// inputs across Google One and Code Assist variants. It checks selected client
// IDs and scopes, including default scopes after an entirely filtered built-in
// list.
func TestEffectiveOAuthConfig_GoogleOne(t *testing.T) {
	tests := []struct {
		name         string      // case identity label
		input        OAuthConfig // configured credential and scope input
		oauthType    string      // selected OAuth variant
		wantClientID string      // expected effective client identifier
		wantScopes   string      // expected normalized scope text
		wantErr      bool        // expected configuration error presence
	}{
		{
			name:         "Google One 使用内置客户端（空配置）",
			input:        OAuthConfig{},
			oauthType:    "google_one",
			wantClientID: GeminiCLIOAuthClientID,
			wantScopes:   DefaultCodeAssistScopes,
			wantErr:      false,
		},
		{
			name: "Google One 使用自定义客户端（传入自定义凭据时使用自定义）",
			input: OAuthConfig{
				ClientID:     "custom-client-id",
				ClientSecret: "custom-client-secret",
			},
			oauthType:    "google_one",
			wantClientID: "custom-client-id",
			wantScopes:   DefaultCodeAssistScopes,
			wantErr:      false,
		},
		{
			name: "Google One 内置客户端 + 自定义 scopes（应过滤受限 scopes）",
			input: OAuthConfig{
				Scopes: "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/generative-language.retriever https://www.googleapis.com/auth/drive.readonly",
			},
			oauthType:    "google_one",
			wantClientID: GeminiCLIOAuthClientID,
			wantScopes:   "https://www.googleapis.com/auth/cloud-platform",
			wantErr:      false,
		},
		{
			name: "Google One 内置客户端 + 仅受限 scopes（应回退到默认）",
			input: OAuthConfig{
				Scopes: "https://www.googleapis.com/auth/generative-language.retriever https://www.googleapis.com/auth/drive.readonly",
			},
			oauthType:    "google_one",
			wantClientID: GeminiCLIOAuthClientID,
			wantScopes:   DefaultCodeAssistScopes,
			wantErr:      false,
		},
		{
			name:         "Code Assist 使用内置客户端",
			input:        OAuthConfig{},
			oauthType:    "code_assist",
			wantClientID: GeminiCLIOAuthClientID,
			wantScopes:   DefaultCodeAssistScopes,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EffectiveOAuthConfig(tt.input, tt.oauthType)
			if (err != nil) != tt.wantErr {
				t.Errorf("EffectiveOAuthConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if got.ClientID != tt.wantClientID {
				t.Errorf("EffectiveOAuthConfig() ClientID = %v, want %v", got.ClientID, tt.wantClientID)
			}
			if got.Scopes != tt.wantScopes {
				t.Errorf("EffectiveOAuthConfig() Scopes = %v, want %v", got.Scopes, tt.wantScopes)
			}
		})
	}
}

// TestEffectiveOAuthConfig_ScopeFiltering combines cloud, profile, Generative
// Language, and Drive scopes for the built-in Google One client. It checks
// retained cloud/profile tokens and removal of both restricted prefix
// families.
func TestEffectiveOAuthConfig_ScopeFiltering(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{
		Scopes: "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/generative-language.retriever https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/userinfo.profile",
	}, "google_one")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}

	// The built-in client retains cloud-platform and userinfo scopes.
	// Generative Language and Drive scopes are filtered.
	if strings.Contains(cfg.Scopes, "generative-language") {
		t.Errorf("使用内置客户端时 Scopes 不应包含 generative-language，实际: %v", cfg.Scopes)
	}
	if strings.Contains(cfg.Scopes, "drive") {
		t.Errorf("使用内置客户端时 Scopes 不应包含 drive，实际: %v", cfg.Scopes)
	}
	if !strings.Contains(cfg.Scopes, "cloud-platform") {
		t.Errorf("Scopes 应包含 cloud-platform，实际: %v", cfg.Scopes)
	}
	if !strings.Contains(cfg.Scopes, "userinfo.email") {
		t.Errorf("Scopes 应包含 userinfo.email，实际: %v", cfg.Scopes)
	}
	if !strings.Contains(cfg.Scopes, "userinfo.profile") {
		t.Errorf("Scopes 应包含 userinfo.profile，实际: %v", cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_OnlyClientID_NoSecret supplies one configured
// credential field. It checks an error names both client fields so partial
// configuration cannot silently combine configured and built-in credentials.
func TestEffectiveOAuthConfig_OnlyClientID_NoSecret(t *testing.T) {
	_, err := EffectiveOAuthConfig(OAuthConfig{
		ClientID: "some-client-id",
	}, "code_assist")
	if err == nil {
		t.Error("只提供 ClientID 不提供 ClientSecret 应该报错")
	}
	if !strings.Contains(err.Error(), "client_id") || !strings.Contains(err.Error(), "client_secret") {
		t.Errorf("错误消息应提及 client_id 和 client_secret，实际: %v", err)
	}
}

// TestEffectiveOAuthConfig_OnlyClientSecret_NoID supplies a secret without its
// client ID. It checks the configuration error mentions both required fields
// before an effective OAuth identity is returned.
func TestEffectiveOAuthConfig_OnlyClientSecret_NoID(t *testing.T) {
	_, err := EffectiveOAuthConfig(OAuthConfig{
		ClientSecret: "some-client-secret",
	}, "code_assist")
	if err == nil {
		t.Error("只提供 ClientSecret 不提供 ClientID 应该报错")
	}
	if !strings.Contains(err.Error(), "client_id") || !strings.Contains(err.Error(), "client_secret") {
		t.Errorf("错误消息应提及 client_id 和 client_secret，实际: %v", err)
	}
}

// TestEffectiveOAuthConfig_AIStudio_DefaultScopes_BuiltinClient leaves
// credentials and scopes absent for AI Studio. It checks the selected built-in
// client uses Code Assist defaults according to the local restricted-scope
// policy.
func TestEffectiveOAuthConfig_AIStudio_DefaultScopes_BuiltinClient(t *testing.T) {
	// The local AI Studio policy selects Code Assist defaults for the built-in
	// client; the configured scope filter classifies Generative Language and Drive
	// prefixes as restricted.
	cfg, err := EffectiveOAuthConfig(OAuthConfig{}, "ai_studio")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	if cfg.Scopes != DefaultCodeAssistScopes {
		t.Errorf("ai_studio + 内置客户端应使用 DefaultCodeAssistScopes，实际: %q", cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_AIStudio_DefaultScopes_CustomClient supplies a
// complete custom credential pair with absent scopes. It checks AI Studio
// receives its retriever defaults while retaining the custom-client path.
func TestEffectiveOAuthConfig_AIStudio_DefaultScopes_CustomClient(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{
		ClientID:     "custom-id",
		ClientSecret: "custom-secret",
	}, "ai_studio")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	if cfg.Scopes != DefaultAIStudioScopes {
		t.Errorf("ai_studio + 自定义客户端应使用 DefaultAIStudioScopes，实际: %q", cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_AIStudio_ScopeNormalization supplies the exact
// legacy Generative Language scope to a custom AI Studio client. It checks the
// retriever spelling appears and the legacy complete token is removed.
func TestEffectiveOAuthConfig_AIStudio_ScopeNormalization(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{
		ClientID:     "custom-id",
		ClientSecret: "custom-secret",
		Scopes:       "https://www.googleapis.com/auth/generative-language https://www.googleapis.com/auth/cloud-platform",
	}, "ai_studio")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	if strings.Contains(cfg.Scopes, "auth/generative-language ") || strings.HasSuffix(cfg.Scopes, "auth/generative-language") {
		// Check complete scope tokens so the retriever suffix is not mistaken for the legacy scope.
		parts := strings.FieldsSeq(cfg.Scopes)
		for p := range parts {
			if p == "https://www.googleapis.com/auth/generative-language" {
				t.Errorf("ai_studio 应将 generative-language 归一化为 generative-language.retriever，实际 scopes: %q", cfg.Scopes)
			}
		}
	}
	if !strings.Contains(cfg.Scopes, "generative-language.retriever") {
		t.Errorf("ai_studio 归一化后应包含 generative-language.retriever，实际: %q", cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_CommaSeparatedScopes supplies comma-separated cloud
// and profile scopes with custom credentials. It checks commas are removed and
// both requested token families remain in the normalized output.
func TestEffectiveOAuthConfig_CommaSeparatedScopes(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{
		ClientID:     "custom-id",
		ClientSecret: "custom-secret",
		Scopes:       "https://www.googleapis.com/auth/cloud-platform,https://www.googleapis.com/auth/userinfo.email",
	}, "code_assist")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	if strings.Contains(cfg.Scopes, ",") {
		t.Errorf("逗号分隔的 scopes 应被归一化为空格分隔，实际: %q", cfg.Scopes)
	}
	if !strings.Contains(cfg.Scopes, "cloud-platform") {
		t.Errorf("归一化后应包含 cloud-platform，实际: %q", cfg.Scopes)
	}
	if !strings.Contains(cfg.Scopes, "userinfo.email") {
		t.Errorf("归一化后应包含 userinfo.email，实际: %q", cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_MixedCommaAndSpaceScopes combines commas and spaces
// between three configured scopes. It checks normalization yields exactly
// three tokens, protecting the shared delimiter handling before filtering.
func TestEffectiveOAuthConfig_MixedCommaAndSpaceScopes(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{
		ClientID:     "custom-id",
		ClientSecret: "custom-secret",
		Scopes:       "https://www.googleapis.com/auth/cloud-platform, https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile",
	}, "code_assist")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	parts := strings.Fields(cfg.Scopes)
	if len(parts) != 3 {
		t.Errorf("归一化后应有 3 个 scope，实际: %d，scopes: %q", len(parts), cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_WhitespaceTriming surrounds custom credentials and
// one scope with whitespace. It checks each value is trimmed before
// configuration is returned for the token and consent clients.
func TestEffectiveOAuthConfig_WhitespaceTriming(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{
		ClientID:     "  custom-id  ",
		ClientSecret: "  custom-secret  ",
		Scopes:       "  https://www.googleapis.com/auth/cloud-platform  ",
	}, "code_assist")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	if cfg.ClientID != "custom-id" {
		t.Errorf("ClientID 应去除前后空白，实际: %q", cfg.ClientID)
	}
	if cfg.ClientSecret != "custom-secret" {
		t.Errorf("ClientSecret 应去除前后空白，实际: %q", cfg.ClientSecret)
	}
	if cfg.Scopes != "https://www.googleapis.com/auth/cloud-platform" {
		t.Errorf("Scopes 应去除前后空白，实际: %q", cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_NoEnvSecret clears the secret environment variable
// and omits configured credentials. It checks the built-in client ID and
// nonblank secret remain available from their constants.
func TestEffectiveOAuthConfig_NoEnvSecret(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{}, "code_assist")
	if err != nil {
		t.Fatalf("不设置环境变量时应回退到内置 secret，实际报错: %v", err)
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		t.Error("ClientSecret 不应为空")
	}
	if cfg.ClientID != GeminiCLIOAuthClientID {
		t.Errorf("ClientID 应回退为内置客户端 ID，实际: %q", cfg.ClientID)
	}
}

// TestEffectiveOAuthConfig_AIStudio_BuiltinClient_CustomScopes provides
// cloud-platform and retriever scopes with the built-in client. It checks
// local filtering removes the restricted family while retaining cloud-platform
// access text.
func TestEffectiveOAuthConfig_AIStudio_BuiltinClient_CustomScopes(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{
		Scopes: "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/generative-language.retriever",
	}, "ai_studio")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	if strings.Contains(cfg.Scopes, "generative-language") {
		t.Errorf("ai_studio + 内置客户端应过滤受限 scopes，实际: %q", cfg.Scopes)
	}
	if !strings.Contains(cfg.Scopes, "cloud-platform") {
		t.Errorf("应保留 cloud-platform scope，实际: %q", cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_UnknownOAuthType_DefaultScopes supplies an unknown
// variant with absent configuration. It checks Code Assist scope defaults are
// returned for the built-in identity rather than an empty scope set.
func TestEffectiveOAuthConfig_UnknownOAuthType_DefaultScopes(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{}, "unknown_type")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	if cfg.Scopes != DefaultCodeAssistScopes {
		t.Errorf("未知 oauthType 应使用 DefaultCodeAssistScopes，实际: %q", cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_EmptyOAuthType_DefaultScopes supplies an empty
// variant and no configuration. It checks the built-in identity uses Code
// Assist scope defaults, protecting the unspecified-variant selection rule.
func TestEffectiveOAuthConfig_EmptyOAuthType_DefaultScopes(t *testing.T) {
	cfg, err := EffectiveOAuthConfig(OAuthConfig{}, "")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	if cfg.Scopes != DefaultCodeAssistScopes {
		t.Errorf("空 oauthType 应使用 DefaultCodeAssistScopes，实际: %q", cfg.Scopes)
	}
}

// TestEffectiveOAuthConfig_CustomClient_NoScopeFiltering supplies a complete
// custom Google One identity with both restricted prefix families. It checks
// those configured scopes remain available because built-in filtering is keyed
// by client ID.
func TestEffectiveOAuthConfig_CustomClient_NoScopeFiltering(t *testing.T) {
	// Custom clients retain scopes that are restricted for the built-in client.
	cfg, err := EffectiveOAuthConfig(OAuthConfig{
		ClientID:     "custom-id",
		ClientSecret: "custom-secret",
		Scopes:       "https://www.googleapis.com/auth/generative-language.retriever https://www.googleapis.com/auth/drive.readonly",
	}, "google_one")
	if err != nil {
		t.Fatalf("EffectiveOAuthConfig() error = %v", err)
	}
	if !strings.Contains(cfg.Scopes, "generative-language.retriever") {
		t.Errorf("自定义客户端不应过滤 generative-language.retriever，实际: %q", cfg.Scopes)
	}
	if !strings.Contains(cfg.Scopes, "drive.readonly") {
		t.Errorf("自定义客户端不应过滤 drive.readonly，实际: %q", cfg.Scopes)
	}
}
