// Legacy 出口只服务未迁入口，算法和状态仍在本包，S15/S16 删除。
package moderation

type LegacyContentModerationKeywordMatcher = contentModerationKeywordMatcher
type LegacyContentModerationKeywordNode = contentModerationKeywordNode
type LegacyContentModerationKeywordEdge = contentModerationKeywordEdge
type LegacyContentModerationKeywordBuildEdge = contentModerationKeywordBuildEdge

func LegacyNewContentModerationKeywordMatcher(keywords []string) *contentModerationKeywordMatcher {
	return newContentModerationKeywordMatcher(keywords)
}
func LegacyNewContentModerationKeywordNode() contentModerationKeywordNode {
	return newContentModerationKeywordNode()
}
func LegacyContentModerationKeywordBuildFirstEdge(node contentModerationKeywordNode) int32 {
	return contentModerationKeywordBuildFirstEdge(node)
}
func LegacyContentModerationKeywordBuildTransition(
	nodes []contentModerationKeywordNode,
	edges []contentModerationKeywordBuildEdge,
	state int32,
	label byte,
) int32 {
	return contentModerationKeywordBuildTransition(nodes, edges, state, label)
}
func LegacyMinKeywordIndex(left, right int32) int32 { return minKeywordIndex(left, right) }
