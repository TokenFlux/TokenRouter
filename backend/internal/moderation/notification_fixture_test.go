// 核对旧风险正文时只投影已确定的展示数据。
package moderation

import "github.com/TokenFlux/TokenRouter/internal/notification"

func buildContentModerationAccountDisabledEmailBody(site string, v *ContentModerationLog, c *ContentModerationConfig) string {
	return notification.BuildContentModerationAccountDisabledEmailBody(site, riskLog(v), riskPolicy(c))
}
