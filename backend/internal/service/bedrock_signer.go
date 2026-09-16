// 旧签名构造仅投影凭据和来源区域。
package service

import (
	"fmt"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

type BedrockSigner = native.BedrockSigner

func NewBedrockSigner(a, b, c, d string) *BedrockSigner { return native.NewBedrockSigner(a, b, c, d) }

// NewBedrockSignerFromAccount 从 Account 凭证创建 BedrockSigner
func NewBedrockSignerFromAccount(account *Account) (*BedrockSigner, error) {
	accessKeyID := account.GetCredential("aws_access_key_id")
	if accessKeyID == "" {
		return nil, fmt.Errorf("aws_access_key_id not found in credentials")
	}
	secretAccessKey := account.GetCredential("aws_secret_access_key")
	if secretAccessKey == "" {
		return nil, fmt.Errorf("aws_secret_access_key not found in credentials")
	}
	// 与模型解析和 HTTP 端点共用来源区域，global 推理不能改变签名范围。
	region := bedrockRuntimeRegion(account)
	sessionToken := account.GetCredential("aws_session_token") // 可选

	return native.NewBedrockSigner(accessKeyID, secretAccessKey, sessionToken, region), nil
}
