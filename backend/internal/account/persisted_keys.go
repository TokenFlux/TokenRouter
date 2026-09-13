// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

// 持久化键沿用现有格式，旧平台消费者通过别名访问。
const CNUsageMonitorSnapshotExtraKey = "cn_usage_monitor_snapshot"
const UpstreamUsageQueryExtraKey = "upstream_usage_query"
const OllamaCloudUsageSnapshotExtraKey = "ollama_cloud_usage_snapshot"
