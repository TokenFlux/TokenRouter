package ops

import "time"

const opsCleanupDefaultSchedule = "0 3 * * *"
const OpsCleanupDefaultSchedule = opsCleanupDefaultSchedule
const opsCleanupDefaultBatchSize = 1000
const OpsCleanupDefaultBatchSize = opsCleanupDefaultBatchSize
const opsCleanupDefaultBatchPause = 200 * time.Millisecond
const OpsCleanupDefaultBatchPause = opsCleanupDefaultBatchPause
