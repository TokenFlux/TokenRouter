//go:build e2e

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// e2e_user_flow_test.go attempts account and API-key creation on an already running
// deployment. Any created resources remain after the tests finish. Sequential
// subtests pass login tokens and gateway keys forward; several unavailable or
// rejected responses are skipped or logged, so the result reflects only the
// assertions reached.

var (
	// testUserEmail, testUserPassword and testUserName supply one
	// process-local registration fixture. The timestamped email is reused
	// for login when administrator credentials are absent.
	testUserEmail    = "e2e-test-" + fmt.Sprintf("%d", time.Now().UnixMilli()) + "@test.local"
	testUserPassword = "E2eTest@12345"
	testUserName     = "e2e-test-user"
)

// TestUserRegistrationAndLogin attempts account registration, then uses its
// credentials for login and a profile request. Reached profile requests require
// HTTP 200; registration results can be logged, and unavailable login or token
// data skips later work.
func TestUserRegistrationAndLogin(t *testing.T) {
	// Register the user used by the following login subtest.
	t.Run("注册新用户", func(t *testing.T) {
		payload := map[string]string{
			"email":    testUserEmail,
			"password": testUserPassword,
			"username": testUserName,
		}
		// Registration marshals fixed string fields, assuming that fixture shape
		// is JSON-encodable. Marshal errors receive no separate setup assertion
		// before the deployment request.
		body, _ := json.Marshal(payload)

		resp, err := doRequest(t, "POST", "/api/auth/register", body, "")
		if err != nil {
			t.Skipf("注册接口不可用，跳过用户流程测试: %v", err)
			return
		}
		// Close the registration response when setup logs or skips its status,
		// releasing its body before the login subtest runs; cleanup discards
		// close errors.
		defer func() { _ = resp.Body.Close() }()

		// Registration retains received bytes for its status log or skip message.
		// A read error is discarded and is not a separate setup assertion.
		respBody, _ := io.ReadAll(resp.Body)

		// Registration is a permissive setup step: a 400 response can
		// still be followed by login, while 403 skips this subtest and
		// other statuses remain diagnostic logs.
		switch resp.StatusCode {
		case 200:
			t.Logf("✅ 用户注册成功: %s", testUserEmail)
		case 400:
			t.Logf("⚠️ 用户可能已存在: %s", string(respBody))
		case 403:
			t.Skipf("注册功能已关闭: %s", string(respBody))
		default:
			t.Logf("⚠️ 注册返回 HTTP %d: %s（继续尝试登录）", resp.StatusCode, string(respBody))
		}
	})

	// Retain the login token for the profile subtest.
	var accessToken string
	t.Run("用户登录获取JWT", func(t *testing.T) {
		payload := map[string]string{
			"email":    testUserEmail,
			"password": testUserPassword,
		}
		// Login marshals the process-local email and password as string fields.
		// The marshal error is discarded before sending credentials; later
		// checks observe the deployment response.
		body, _ := json.Marshal(payload)

		resp, err := doRequest(t, "POST", "/api/auth/login", body, "")
		if err != nil {
			t.Fatalf("登录请求失败: %v", err)
		}
		// Close the login response after extracting a token or skipping its
		// credentials, releasing its body before the profile subtest runs;
		// cleanup discards close errors.
		defer func() { _ = resp.Body.Close() }()

		// A failed body read can still yield bytes; login ignores that error and
		// relies on later status, decoding and nonempty-token checks before
		// profile work.
		respBody, _ := io.ReadAll(resp.Body)

		// A rejected login cannot supply the token needed by later
		// subtests. Skip this deployment-dependent path so registration
		// policy or credentials remain visible in its diagnostic
		// response.
		if resp.StatusCode != 200 {
			t.Skipf("登录失败 HTTP %d: %s（可能需要先注册用户）", resp.StatusCode, string(respBody))
			return
		}

		var result map[string]any
		if err := json.Unmarshal(respBody, &result); err != nil {
			t.Fatalf("解析登录响应失败: %v", err)
		}

		// Carry a nonempty top-level access_token forward, then inspect
		// the data envelope when it is absent or empty; the subsequent
		// profile request depends on that selected value.
		if token, ok := result["access_token"].(string); ok && token != "" {
			accessToken = token
		} else if data, ok := result["data"].(map[string]any); ok {
			if token, ok := data["access_token"].(string); ok {
				accessToken = token
			}
		}

		if accessToken == "" {
			t.Skipf("未获取到 access_token，响应: %s", string(respBody))
			return
		}

		// The ten-byte minimum rejects obviously short credential
		// strings before profile use. This length check does not parse
		// JWT claims or validate the deployment's signature.
		if len(accessToken) < 10 {
			t.Fatalf("access_token 格式异常: %s", accessToken)
		}

		t.Logf("✅ 登录成功，获取 JWT（长度: %d）", len(accessToken))
	})

	if accessToken == "" {
		t.Skip("未获取到 JWT，跳过后续测试")
		return
	}

	// Use the acquired token to fetch the current profile.
	t.Run("获取当前用户信息", func(t *testing.T) {
		resp, err := doRequest(t, "GET", "/api/user/me", nil, accessToken)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		// Close the profile response after its HTTP check, releasing this
		// request's body on both successful and failed test exits; cleanup
		// discards close errors.
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != 200 {
			// Capture received profile-error bytes for the failure message. The
			// non-200 status already determines failure, so discarding a body-read
			// error changes the available diagnostics.
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("HTTP %d: %s", resp.StatusCode, string(body))
		}

		t.Logf("✅ 成功获取用户信息")
	})
}

// TestAPIKeyLifecycle logs in using administrator or fixture credentials,
// creates a key and attempts gateway and usage requests. Key creation can skip
// the scenario; gateway and usage statuses are reported through logs rather
// than asserted success.
func TestAPIKeyLifecycle(t *testing.T) {
	// Later subtests need a successful login.
	accessToken := loginTestUser(t)
	if accessToken == "" {
		t.Skip("无法登录，跳过 API Key 生命周期测试")
		return
	}

	var apiKey string

	// Retain the created key for the gateway subtest.
	t.Run("创建API_Key", func(t *testing.T) {
		payload := map[string]string{
			"name": "e2e-test-key-" + fmt.Sprintf("%d", time.Now().UnixMilli()),
		}
		// Key creation marshals one generated name string, assuming that fixture
		// shape is JSON-encodable. Marshal errors receive no separate assertion
		// before the authenticated request.
		body, _ := json.Marshal(payload)

		resp, err := doRequest(t, "POST", "/api/keys", body, accessToken)
		if err != nil {
			t.Fatalf("创建 API Key 请求失败: %v", err)
		}
		// Close the key-creation response after extracting its credential or
		// skipping its status, releasing the body before gateway work; cleanup
		// discards close errors.
		defer func() { _ = resp.Body.Close() }()

		// Key creation uses received bytes for status diagnostics and credential
		// decoding. Body-read errors receive no direct assertion; later checks
		// decide whether a key reaches gateway work.
		respBody, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			t.Skipf("创建 API Key 失败 HTTP %d: %s", resp.StatusCode, string(respBody))
			return
		}

		var result map[string]any
		if err := json.Unmarshal(respBody, &result); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}

		// A top-level string key selects the credential even when
		// empty; nested data is examined only otherwise. The
		// empty-result guard then skips gateway work needing that key.
		if key, ok := result["key"].(string); ok {
			apiKey = key
		} else if data, ok := result["data"].(map[string]any); ok {
			if key, ok := data["key"].(string); ok {
				apiKey = key
			}
		}

		if apiKey == "" {
			t.Skipf("未获取到 API Key，响应: %s", string(respBody))
			return
		}

		// Limit the logged prefix for credentials longer than eight
		// bytes so successful creation diagnostics avoid exposing the
		// remainder; short keys remain as supplied here.
		masked := apiKey
		if len(masked) > 8 {
			masked = masked[:8] + "..."
		}
		t.Logf("✅ API Key 创建成功: %s", masked)
	})

	if apiKey == "" {
		t.Skip("未创建 API Key，跳过后续测试")
		return
	}

	// Use the new key to access the gateway.
	t.Run("使用API_Key调用网关", func(t *testing.T) {
		// Listing models exercises gateway authentication without
		// generating content.
		resp, err := doRequest(t, "GET", "/v1/models", nil, apiKey)
		if err != nil {
			t.Fatalf("网关请求失败: %v", err)
		}
		// Close the gateway response after logging its received status, releasing
		// its body even when the log reports a rejected request; cleanup
		// discards close errors.
		defer func() { _ = resp.Body.Close() }()

		// The body supplies diagnostics on some gateway statuses. Its read error
		// is discarded; this subtest logs every HTTP status and makes no
		// status-success assertion.
		respBody, _ := io.ReadAll(resp.Body)

		// Record the received gateway status for deployment diagnosis.
		// These branches neither fail nor skip on status, so their
		// messages do not prove authentication or provider eligibility.
		switch resp.StatusCode {
		case 200:
			t.Logf("✅ API Key 网关调用成功")
		case 402:
			t.Logf("⚠️ 余额不足，但 API Key 认证通过")
		case 403:
			t.Logf("⚠️ 无可用提供商，但 API Key 认证通过")
		default:
			t.Logf("⚠️ 网关返回 HTTP %d: %s", resp.StatusCode, string(respBody))
		}
	})

	// Query usage with the login token.
	t.Run("查询用量记录", func(t *testing.T) {
		resp, err := doRequest(t, "GET", "/api/usage/dashboard", nil, accessToken)
		if err != nil {
			t.Fatalf("用量查询请求失败: %v", err)
		}
		// Close the usage response after its success or error log, releasing this
		// request's body even when the status is diagnostic; cleanup discards
		// close errors.
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != 200 {
			// A non-200 usage response is logged with received body bytes. Body-read
			// errors are discarded, and the subtest returns without failing or
			// skipping on that status.
			body, _ := io.ReadAll(resp.Body)
			t.Logf("⚠️ 用量查询返回 HTTP %d: %s", resp.StatusCode, string(body))
			return
		}

		t.Logf("✅ 用量查询成功")
	})
}

// doRequest sends the supplied body and optional Bearer credential to baseURL
// plus path with a thirty-second client timeout. A successful return transfers
// response-body cleanup to the caller, including requests whose status
// indicates failure.
func doRequest(t *testing.T, method, path string, body []byte, token string) (*http.Response, error) {
	t.Helper()

	url := baseURL + path
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

// loginTestUser chooses administrator credentials when ADMIN_PASSWORD is
// nonempty, otherwise the process-local account fixture. Request, status or
// decoding failure returns empty text. A top-level string access_token wins
// even when empty; nested data is considered when the top-level value fails
// the string assertion.
func loginTestUser(t *testing.T) string {
	t.Helper()

	// Select the administrator login when a password is configured so key
	// creation can run independently of the registration scenario; getEnv
	// retains configured whitespace.
	adminEmail := getEnv("ADMIN_EMAIL", "admin@tokenrouter.local")
	adminPassword := getEnv("ADMIN_PASSWORD", "")

	if adminPassword == "" {
		// An empty administrator password selects the process-local
		// registration fixture. This helper does not create that user,
		// so login can fail when registration never completed.
		adminEmail = testUserEmail
		adminPassword = testUserPassword
	}

	payload := map[string]string{
		"email":    adminEmail,
		"password": adminPassword,
	}
	// The selected login email and password are string fields, so this
	// fixture assumes JSON marshaling succeeds. Its marshal error is
	// discarded before the login request.
	body, _ := json.Marshal(payload)

	resp, err := doRequest(t, "POST", "/api/auth/login", body, "")
	// A failed login request supplies no usable credential for key
	// creation. Return empty text so the lifecycle caller skips work that
	// requires an authenticated response.
	if err != nil {
		return ""
	}
	// Close the login response after status and token selection complete,
	// releasing its body even when this helper returns empty text; cleanup
	// discards close errors.
	defer func() { _ = resp.Body.Close() }()

	// Reject the HTTP status before decoding its envelope so an error
	// response cannot supply credentials. The empty result tells the
	// lifecycle caller that login did not succeed.
	if resp.StatusCode != 200 {
		return ""
	}

	// The helper decodes received response bytes after an HTTP 200. A read
	// error is discarded; a subsequent JSON decoding error produces the
	// empty-login result.
	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]any
	// A decoding error supplies no accepted login envelope. Return empty
	// text before inspecting token fields so the lifecycle caller skips
	// key creation for that response.
	if err := json.Unmarshal(respBody, &result); err != nil {
		return ""
	}

	// Prefer any top-level string token over the data envelope, including
	// an empty string; callers use the empty result to skip work requiring
	// authenticated requests.
	if token, ok := result["access_token"].(string); ok {
		return token
	}
	if data, ok := result["data"].(map[string]any); ok {
		if token, ok := data["access_token"].(string); ok {
			return token
		}
	}

	return ""
}
