package httpapi

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// AuthenticationOptions 仅描述 HTTP 输入和观察端口；网关转换/计费仍由后续执行层拥有。
type AuthenticationOptions struct {
	Google          bool
	Context         func(*gin.Context) context.Context
	ClientIP        func(*gin.Context) string
	AbuseClientKey  func(*gin.Context) string
	NonConsuming    func(*gin.Context) bool
	Rejected        func(*gin.Context, string)
	BusinessLimited func(*gin.Context, string)
	Loaded          func(*gin.Context, *apikey.APIKey)
}

func AbortWithError(c *gin.Context, status int, code, message string) {
	httpx.AbortWithError(c, status, code, message)
}
func (o AuthenticationOptions) reject(c *gin.Context, reason string) {
	if o.Rejected != nil {
		o.Rejected(c, reason)
	}
}
func (o AuthenticationOptions) business(c *gin.Context, reason string) {
	if o.BusinessLimited != nil {
		o.BusinessLimited(c, reason)
	}
}
func (o AuthenticationOptions) abort(c *gin.Context, status int, code, message string) {
	if o.Google {
		AbortGoogleError(c, status, message)
	} else {
		AbortWithError(c, status, code, message)
	}
}

// Authenticate 提取通用/Google 凭据并运行唯一核心认证，不读取请求体或提前执行路由策略。
func Authenticate(c *gin.Context, auth *apikey.APIKeyService, o AuthenticationOptions) (*apikey.AccessSnapshot, bool) {
	clientKey := ""
	if o.AbuseClientKey != nil {
		clientKey = o.AbuseClientKey(c)
	}
	invalid := func() { auth.RecordInvalidAuthFailure(clientKey) }
	if retry, blocked := auth.CheckInvalidAuthAbuse(clientKey); blocked {
		seconds := int(math.Ceil(retry.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
		o.reject(c, "invalid_auth_rate_limited")
		o.abort(c, 429, "INVALID_AUTH_RATE_LIMITED", "Too many invalid authentication attempts; retry later")
		return nil, false
	}
	if HeadersTooLarge(c) {
		invalid()
		o.reject(c, "invalid_api_key")
		o.abort(c, 401, "INVALID_API_KEY", "Invalid API key")
		return nil, false
	}
	if o.Google {
		if strings.TrimSpace(c.Query("api_key")) != "" {
			invalid()
			o.reject(c, "query_api_key_deprecated")
			o.abort(c, 400, "api_key_in_query_deprecated", "Query parameter api_key is deprecated. Use Authorization header or key instead.")
			return nil, false
		}
	} else if strings.TrimSpace(c.Query("key")) != "" || strings.TrimSpace(c.Query("api_key")) != "" {
		invalid()
		o.reject(c, "query_api_key_deprecated")
		o.abort(c, 400, "api_key_in_query_deprecated", "API key in query parameter is deprecated. Please use Authorization header instead.")
		return nil, false
	}
	credential := ""
	if o.Google {
		credential = ExtractGoogleCredential(c)
	} else {
		if header := c.GetHeader("Authorization"); header != "" {
			parts := strings.SplitN(header, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				credential = strings.TrimSpace(parts[1])
			}
		}
		if credential == "" {
			credential = c.GetHeader("x-api-key")
		}
		if credential == "" {
			credential = c.GetHeader("x-goog-api-key")
		}
	}
	if credential == "" {
		invalid()
		if HasCredentialInput(c) {
			o.reject(c, "invalid_api_key")
		} else {
			o.reject(c, "api_key_required")
		}
		message := "API key is required in Authorization header (Bearer scheme), x-api-key header, or x-goog-api-key header"
		if o.Google {
			message = "API key is required"
		}
		o.abort(c, 401, "API_KEY_REQUIRED", message)
		return nil, false
	}
	if len(credential) > apikey.MaxAPIKeyCredentialBytes {
		invalid()
		o.reject(c, "invalid_api_key")
		o.abort(c, 401, "INVALID_API_KEY", "Invalid API key")
		return nil, false
	}
	ctx := c.Request.Context()
	if o.Context != nil {
		ctx = o.Context(c)
	}
	ip := ""
	if o.ClientIP != nil {
		ip = o.ClientIP(c)
	}
	checkLimits := true
	if o.NonConsuming != nil {
		checkLimits = !o.NonConsuming(c)
	}
	access, err := auth.Authenticate(ctx, credential, apikey.AuthenticationInput{ClientIP: ip, CheckMemberLimits: checkLimits})
	if access != nil && o.Loaded != nil {
		o.Loaded(c, access.KeyView())
	}
	if err == nil {
		return access, true
	}
	var failure *apikey.AuthenticationFailure
	if !errors.As(err, &failure) {
		o.abort(c, 500, "INTERNAL_ERROR", "Failed to validate API key")
		return nil, false
	}
	switch failure.Kind {
	case apikey.AuthenticationLookup:
		switch {
		case errors.Is(err, apikey.ErrAPIKeyNotFound):
			invalid()
			o.reject(c, "invalid_api_key")
			o.abort(c, 401, "INVALID_API_KEY", "Invalid API key")
		case errors.Is(err, apikey.ErrGroupDisabledForUser):
			o.business(c, "api_key_group_unavailable")
			o.abort(c, 403, "GROUP_DISABLED_FOR_USER", "API Key 所属公开分组已被禁用")
		case errors.Is(err, apikey.ErrAPIKeyAuthOverloaded):
			o.reject(c, "api_key_auth_overloaded")
			o.abort(c, 503, "API_KEY_AUTH_OVERLOADED", "API key authentication is temporarily unavailable")
		default:
			if !o.abortTeam(c, err) {
				o.abort(c, 500, "INTERNAL_ERROR", "Failed to validate API key")
			}
		}
	case apikey.AuthenticationDisabled:
		o.reject(c, "api_key_disabled")
		o.abort(c, 401, "API_KEY_DISABLED", "API key is disabled")
	case apikey.AuthenticationTeam:
		if !o.abortTeam(c, err) {
			o.abort(c, 500, "INTERNAL_ERROR", "Failed to validate team API key")
		}
	case apikey.AuthenticationMemberLimit:
		if !o.abortTeam(c, err) {
			o.abort(c, 500, "INTERNAL_ERROR", "Failed to validate team member limits")
		}
	case apikey.AuthenticationIP:
		o.business(c, "api_key_ip_restriction")
		o.reject(c, "ip_restricted")
		o.abort(c, 403, "ACCESS_DENIED", err.Error())
	case apikey.AuthenticationUserMissing:
		o.abort(c, 401, "USER_NOT_FOUND", "User associated with API key not found")
	case apikey.AuthenticationUserInactive:
		o.reject(c, "user_inactive")
		o.abort(c, 401, "USER_INACTIVE", "User account is not active")
	}
	return nil, false
}
func (o AuthenticationOptions) abortTeam(c *gin.Context, err error) bool {
	if !o.Google {
		return AbortTeamError(c, err)
	}
	status, message, ok := GoogleTeamError(err)
	if ok {
		AbortGoogleError(c, status, message)
	}
	return ok
}

// SetAccessPrincipal 只在路由/计费门禁全部通过后记录行为身份，Key 不赋予管理员会话权限。
func SetAccessPrincipal(c *gin.Context, a *apikey.AccessSnapshot) {
	if a == nil {
		return
	}
	c.Set("apikey_access_snapshot", a)
	identityhttp.SetAuthenticatedPrincipal(c, identity.Principal{UserID: a.ActorUserID, CredentialKind: "api_key"})
}
