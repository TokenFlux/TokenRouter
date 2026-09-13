// 本文件维护 accessview 的所属能力；兼容入口复用唯一实现。
package accessview

type ReasoningEffortMapping struct {
	From      string `json:"from"`
	To        string `json:"to"`
	MatchType string `json:"match_type,omitempty"`
	Model     string `json:"model,omitempty"`
}
