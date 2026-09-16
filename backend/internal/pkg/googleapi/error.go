package googleapi

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// 错误 wire 类型由 protocol/google 唯一拥有，激活诊断留 S09 迁移。
type ErrorResponse = google.ErrorResponse
type ErrorDetail = google.ErrorDetail
type ErrorDetailInfo = google.ErrorDetailInfo
type ErrorHelp = google.ErrorHelp
type HelpLink = google.HelpLink

func ParseError(body string) (*ErrorResponse, error) { return google.ParseError(body) }
