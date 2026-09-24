package httpapi

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/gin-gonic/gin"
)

func compatResponseKey(c *gin.Context, account *gatewayprovider.ExecutionAccount, promptCacheKey string) string {
	key := strings.TrimSpace(promptCacheKey)
	if account == nil || key == "" {
		return ""
	}
	apiKeyID := int64(0)
	if c != nil {
		apiKeyID = APIKeyIDFromContext(c)
	}
	return session.CompatResponseKey(account.Record.ID, apiKeyID, key)
}
