package httpapi

import accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

// OpenAIImagesExecutor 组合图片单次执行与响应处理，不持有账号选择或结算循环。
type OpenAIImagesExecutor struct {
	Requests *OpenAIRequests
	Output   *OpenAIResponseOutput
	Cooldown *accountprovider.ImageToolCooldown
	Enter    func() (func(), error)
}
