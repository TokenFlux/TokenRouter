package bedrock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// BedrockSigner 使用 AWS SigV4 对 Bedrock 请求签名
type BedrockSigner struct {
	credentials aws.Credentials
	Region      string
	signer      *v4.Signer
}

// NewBedrockSigner 创建 BedrockSigner
func NewBedrockSigner(accessKeyID, secretAccessKey, sessionToken, region string) *BedrockSigner {
	return &BedrockSigner{
		credentials: aws.Credentials{
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
			SessionToken:    sessionToken,
		},
		Region: region,
		signer: v4.NewSigner(),
	}
}

// SignRequest 对 HTTP 请求进行 SigV4 签名
// 重要约束：调用此方法前，req 应只包含 AWS 相关的 header（如 Content-Type、Accept）。
// 非 AWS header（如 anthropic-beta）会参与签名计算，如果 Bedrock 服务端不识别这些 header，
// 签名验证可能失败。litellm 通过 _filter_headers_for_aws_signature 实现头过滤，
// 当前实现中 buildUpstreamRequestBedrock 仅设置了 Content-Type 和 Accept，因此是安全的。
func (s *BedrockSigner) SignRequest(ctx context.Context, req *http.Request, body []byte) error {
	payloadHash := SHA256Hash(body)
	return s.signer.SignHTTP(ctx, s.credentials, req, payloadHash, "bedrock", s.Region, time.Now())
}

func SHA256Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
