package repository

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/service"
)

func opsUpstreamContextSQL(upstreamStatusExpr, upstreamErrorsExpr, ownerExpr string) string {
	upstreamErrorsPresentSQL := fmt.Sprintf(`COALESCE(
  CASE
    WHEN jsonb_typeof(COALESCE(NULLIF(%s, 'null'::jsonb), '[]'::jsonb)) = 'array'
      THEN jsonb_array_length(COALESCE(NULLIF(%s, 'null'::jsonb), '[]'::jsonb))
    ELSE 0
  END,
  0
) > 0`, upstreamErrorsExpr, upstreamErrorsExpr)

	return fmt.Sprintf("(%s IS NOT NULL OR %s OR LOWER(COALESCE(%s, '')) = 'provider')", upstreamStatusExpr, upstreamErrorsPresentSQL, ownerExpr)
}

func opsIgnoredStatusCodeSQL(statusExpr string, ignoredStatusCodes []int) string {
	codes := service.NormalizeOpsIgnoredStatusCodes(ignoredStatusCodes)
	if len(codes) == 0 {
		return "FALSE"
	}
	parts := make([]string, 0, len(codes))
	for _, code := range codes {
		parts = append(parts, strconv.Itoa(code))
	}
	return fmt.Sprintf("COALESCE(%s, 0) IN (%s)", statusExpr, strings.Join(parts, ", "))
}

func opsClientSideStatusExcludedSQL(statusExpr, upstreamStatusExpr, upstreamErrorsExpr, ownerExpr string, ignoredStatusCodes []int) string {
	// 客户端侧忽略状态码通常是调用方凭据或权限问题；存在上游上下文时仍计入 SLA。
	return fmt.Sprintf("(%s AND NOT %s)", opsIgnoredStatusCodeSQL(statusExpr, ignoredStatusCodes), opsUpstreamContextSQL(upstreamStatusExpr, upstreamErrorsExpr, ownerExpr))
}

func opsBusinessLimitedSQL(statusExpr, upstreamStatusExpr, upstreamErrorsExpr, ownerExpr, businessExpr string, ignoredStatusCodes []int) string {
	return fmt.Sprintf("(COALESCE(%s, false) OR %s)", businessExpr, opsClientSideStatusExcludedSQL(statusExpr, upstreamStatusExpr, upstreamErrorsExpr, ownerExpr, ignoredStatusCodes))
}

func opsSLACountableSQL(statusExpr, upstreamStatusExpr, upstreamErrorsExpr, ownerExpr, businessExpr string, ignoredStatusCodes []int) string {
	return fmt.Sprintf("(NOT COALESCE(%s, false) AND NOT %s)", businessExpr, opsClientSideStatusExcludedSQL(statusExpr, upstreamStatusExpr, upstreamErrorsExpr, ownerExpr, ignoredStatusCodes))
}
