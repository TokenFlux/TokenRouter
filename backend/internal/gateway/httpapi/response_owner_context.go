package httpapi

import "github.com/gin-gonic/gin"

const responseOwnerContextKey = "openai_http_response_owner"

// HTTPResponseOwner 只记录成功续接应绑定的下游主体，不含上游账号凭据。
type HTTPResponseOwner struct{ UserID, APIKeyID int64 }

// SetHTTPResponseOwner 只接受已通过认证入口提供的有效标识，保留原上下文字段。
func SetHTTPResponseOwner(c *gin.Context, userID, keyID int64) {
	if c == nil || userID <= 0 || keyID <= 0 {
		return
	}
	c.Set(responseOwnerContextKey, HTTPResponseOwner{UserID: userID, APIKeyID: keyID})
}

// ResponseOwnerFromContext 不从未经认证的 Key 加载投影补造归属。
func ResponseOwnerFromContext(c *gin.Context) (HTTPResponseOwner, bool) {
	value, ok := c.Get(responseOwnerContextKey)
	if !ok {
		return HTTPResponseOwner{}, false
	}
	owner, ok := value.(HTTPResponseOwner)
	return owner, ok && owner.UserID > 0 && owner.APIKeyID > 0
}
