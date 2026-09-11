//go:build unit

package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// TestBatchImageBillingEntityTable 校验计费实体表白名单：
// 批量图片作业落 batch_image_jobs，创作台任务落 creative_runs，与幂等指纹无关。
func TestBatchImageBillingEntityTable(t *testing.T) {
	table, idColumn, err := batchImageBillingEntityTable(nil)
	require.NoError(t, err)
	require.Equal(t, "batch_image_jobs", table)
	require.Equal(t, "batch_id", idColumn)

	table, idColumn, err = batchImageBillingEntityTable(&billing.TaskFundsCommand{Task: billing.TaskReference{ID: "imgbatch_x"}})
	require.NoError(t, err)
	require.Equal(t, "batch_image_jobs", table)
	require.Equal(t, "batch_id", idColumn)

	table, idColumn, err = batchImageBillingEntityTable(&billing.TaskFundsCommand{Task: billing.TaskReference{ID: "crun_x", Kind: billing.TaskCreative}})
	require.NoError(t, err)
	require.Equal(t, "creative_runs", table)
	require.Equal(t, "run_id", idColumn)
}

// TestBatchImageHoldClaimRequestID 校验预占认领 id 的实体感知：
// 创作台任务查 creative_hold 前缀的 dedup 记录，批量图片作业查 batch_image_hold。
func TestBatchImageHoldClaimRequestID(t *testing.T) {
	require.Equal(t, "", batchImageHoldClaimRequestID(nil))
	require.Equal(t, "batch_image_hold:imgbatch_x", batchImageHoldClaimRequestID(&billing.TaskFundsCommand{Task: billing.TaskReference{ID: "imgbatch_x"}}))
	require.Equal(t, "creative_hold:crun_x", batchImageHoldClaimRequestID(&billing.TaskFundsCommand{Task: billing.TaskReference{ID: "crun_x", Kind: billing.TaskCreative}}))
}
