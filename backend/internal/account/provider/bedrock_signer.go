package provider

import (
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

// NewBedrockSignerFromAccount 从 Account 凭证创建 BedrockSigner
func NewBedrockSignerFromAccount(value *account.Record) (*bedrock.BedrockSigner, error) {
	accessKeyID := value.GetCredential("aws_access_key_id")
	if accessKeyID == "" {
		return nil, fmt.Errorf("aws_access_key_id not found in credentials")
	}
	secretAccessKey := value.GetCredential("aws_secret_access_key")
	if secretAccessKey == "" {
		return nil, fmt.Errorf("aws_secret_access_key not found in credentials")
	}
	// 与模型解析和 HTTP 端点共用来源区域，global 推理不能改变签名范围。
	region := bedrock.BedrockRuntimeRegion(&bedrock.RouteInput{Region: value.GetCredential("aws_region"), ForceGlobal: value.GetCredential("aws_force_global") == "true"})
	sessionToken := value.GetCredential("aws_session_token") // 可选

	return bedrock.NewBedrockSigner(accessKeyID, secretAccessKey, sessionToken, region), nil
}
