package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// AccountExtraImagesURLToB64JSON 保留既有账号设置键。
const AccountExtraImagesURLToB64JSON = "images_url_to_b64_json"

// ImagesURLToB64JSONEnabled 返回账户是否开启 URL 到 base64 的图片回填。
func ImagesURLToB64JSONEnabled(account *ExecutionAccount) bool {
	return account != nil && account.Record.Platform == capability.PlatformOpenAI && account.Record.Type == capability.AccountTypeAPIKey && ExecutionProtocolRecord(account).GetExtraBool(AccountExtraImagesURLToB64JSON)
}
