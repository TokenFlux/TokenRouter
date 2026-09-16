// 旧审核 handler 类型名仅为路由汇总兼容，生产构造直接在 app。
package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/moderation/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type ContentModerationHandler = httpapi.ContentModerationHandler

func NewContentModerationHandler(s *service.ContentModerationService) *ContentModerationHandler {
	return httpapi.NewContentModerationHandler(s.ContentModerationService)
}
