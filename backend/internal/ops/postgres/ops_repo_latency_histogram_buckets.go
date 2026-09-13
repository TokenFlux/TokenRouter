package postgres

import (
	"fmt"
	"strings"
)

// latencyHistogramBucketIndexCaseExpr 使用参数占位符生成桶索引，避免将用户输入拼入 SQL。
func latencyHistogramBucketIndexCaseExpr(column string, firstPlaceholder, boundaryCount int) string {
	var sb strings.Builder
	_, _ = sb.WriteString("CASE\n")

	for index := 0; index < boundaryCount; index++ {
		fmt.Fprintf(&sb, "\tWHEN %s < $%d THEN %d\n", column, firstPlaceholder+index, index)
	}

	fmt.Fprintf(&sb, "\tELSE %d\n", boundaryCount)
	_, _ = sb.WriteString("END")
	return sb.String()
}

// latencyHistogramBucketLabels 按边界生成六个稳定有序的展示标签。
func latencyHistogramBucketLabels(boundaries []int64) []string {
	labels := make([]string, 0, len(boundaries)+1)
	lower := int64(0)
	for _, upper := range boundaries {
		labels = append(labels, fmt.Sprintf("%d-%dms", lower, upper))
		lower = upper
	}
	labels = append(labels, fmt.Sprintf("%dms+", lower))
	return labels
}
