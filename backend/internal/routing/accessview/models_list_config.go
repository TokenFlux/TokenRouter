// 本文件维护 accessview 的所属能力；兼容入口复用唯一实现。
package accessview

// GroupModelsListConfig 控制可选的自定义 /v1/models 响应列表。
type GroupModelsListConfig struct {
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models,omitempty"`
}
