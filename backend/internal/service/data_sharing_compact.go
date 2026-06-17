package service

import (
	"encoding/json"
	"sort"
	"strings"
)

// CompactDataShareMessages 压缩 Responses/Codex 每轮请求重复携带的历史消息。
func CompactDataShareMessages(messages []map[string]any) []map[string]any {
	out := messages
	for pass := 0; pass < dataShareCompactFixedPointMaxPasses; pass++ {
		next := compactDataShareMessagesOnce(out)
		if len(next) == len(out) {
			return next
		}
		out = next
		if len(out) < dataShareLongReplayMinMessages*2 {
			return out
		}
	}
	return out
}

// compactDataShareMessagesOnce 执行一轮压缩；公开入口会在固定上限内重复调用直到长度稳定。
func compactDataShareMessagesOnce(messages []map[string]any) []map[string]any {
	messages = dataShareCompactTrailingReplayBlock(messages)
	out := make([]map[string]any, 0, len(messages))
	outIdentities := make([]string, 0, len(messages))
	outIdentityPositions := map[string][]int{}
	outIdentityAt := func(index int) string {
		if outIdentities[index] == "" {
			outIdentities[index] = dataShareMessageIdentity(out[index])
		}
		return outIdentities[index]
	}
	messageIdentities := make([]string, len(messages))
	messageIdentityAt := func(index int) string {
		if messageIdentities[index] == "" {
			messageIdentities[index] = dataShareMessageIdentity(messages[index])
		}
		return messageIdentities[index]
	}
	seenToolCalls := map[string]struct{}{}
	seenToolResults := map[string]struct{}{}
	seenAssistantText := map[string]int{}
	assistantTextEpoch := 0
	for i := 0; i < len(messages); {
		if len(out) > 0 {
			if replay := dataShareReplaySkipLen(
				out,
				len(messages),
				i,
				outIdentityAt,
				messageIdentityAt,
				outIdentityPositions,
				seenToolCalls,
				seenToolResults,
			); replay >= dataShareReplayOverlapMinMessages {
				i += replay
				continue
			}
		}
		msg := messages[i]
		if dataShareMessageAlreadySeen(msg, seenToolCalls, seenToolResults) {
			i++
			continue
		}
		if dataShareAssistantTextEchoWindowReset(msg) {
			assistantTextEpoch++
		}
		if dataShareCommentaryEchoAlreadySeen(msg, seenAssistantText, assistantTextEpoch) {
			i++
			continue
		}
		rememberDataShareMessage(msg, seenToolCalls, seenToolResults)
		rememberDataShareAssistantTextMessage(msg, seenAssistantText, assistantTextEpoch)
		out = append(out, msg)
		identity := messageIdentityAt(i)
		outIdentities = append(outIdentities, identity)
		outIdentityPositions[identity] = append(outIdentityPositions[identity], len(out)-1)
		i++
	}
	return dataShareCompactGlobalReplayWindows(dataShareCompactTrailingReplayBlock(dataShareCompactAdjacentReplayBlocks(out)))
}

func dataShareCompactAdjacentReplayBlocks(messages []map[string]any) []map[string]any {
	if len(messages) < dataShareLongReplayMinMessages*2 {
		return messages
	}
	// 相邻长重复块更符合 replay 污染形态，默认压成一份；非相邻纯文本重复由全局窗口逻辑保守处理。
	out := cloneBufferedDataShareMaps(messages)
	for pass := 0; pass < dataShareAdjacentReplayCompactMaxPasses; pass++ {
		keys := dataShareMessageIdentityKeys(out)
		keyHash := dataShareNewReplayRangeHash(keys)
		index := dataShareReplayWindowIndex(keys)
		compact := make([]map[string]any, 0, len(out))
		changed := false
		for i := 0; i < len(out); {
			if matchLen := dataShareAdjacentReplayBlockLen(keys, keyHash, index, i); matchLen >= dataShareLongReplayMinMessages {
				runEnd := i + matchLen
				for runEnd+matchLen <= len(out) && dataShareKeysEqualHashed(keys, keyHash, runEnd-matchLen, runEnd, matchLen) {
					runEnd += matchLen
				}
				if runEnd > i+matchLen && dataShareHasEarlierReplayBlock(keys, keyHash, index, i, matchLen) {
					// replay run 自身相邻重复且更早历史已有同一块时，整段 run 都是污染副本。
					i = runEnd
					changed = true
					continue
				}
				if runEnd > i+matchLen {
					compact = append(compact, cloneBufferedDataShareMaps(out[i:i+matchLen])...)
					i = runEnd
					changed = true
					continue
				}
				compact = append(compact, cloneBufferedDataShareMaps(out[i:runEnd])...)
				i = runEnd
				continue
			}
			compact = append(compact, cloneDataShareMap(out[i]))
			i++
		}
		out = compact
		if !changed || len(out) < dataShareLongReplayMinMessages*2 {
			return out
		}
	}
	return out
}

func dataShareAdjacentReplayBlockLen(keys []string, keyHash dataShareReplayRangeHash, index map[string][]int, start int) int {
	candidates := index[dataShareReplayWindowKey(keys, start)]
	if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
		return 0
	}
	best := 0
	for _, other := range candidates {
		if other <= start {
			continue
		}
		blockLen := other - start
		if blockLen < dataShareLongReplayMinMessages || start+blockLen*2 > len(keys) {
			continue
		}
		if dataShareKeysEqualHashed(keys, keyHash, start, other, blockLen) && blockLen > best {
			best = blockLen
		}
	}
	return best
}

func dataShareHasEarlierReplayBlock(keys []string, keyHash dataShareReplayRangeHash, index map[string][]int, start int, length int) bool {
	if start <= 0 || length < dataShareLongReplayMinMessages {
		return false
	}
	candidates := index[dataShareReplayWindowKey(keys, start)]
	if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
		return false
	}
	for _, other := range candidates {
		if other >= start {
			continue
		}
		if dataShareKeysEqualHashed(keys, keyHash, other, start, length) {
			return true
		}
	}
	return false
}

func dataShareCompactTrailingReplayBlock(messages []map[string]any) []map[string]any {
	if len(messages) < dataShareLongReplayMinMessages*2 {
		return messages
	}
	keys := dataShareMessageIdentityKeys(messages)
	keyHash := dataShareNewReplayRangeHash(keys)
	index := dataShareReplayWindowIndex(keys)
	for suffixStart := dataShareLongReplayMinMessages; suffixStart <= len(keys)-dataShareLongReplayMinMessages; suffixStart++ {
		suffixLen := len(keys) - suffixStart
		candidates := index[dataShareReplayWindowKey(keys, suffixStart)]
		if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
			continue
		}
		for _, pos := range candidates {
			if pos >= suffixStart {
				continue
			}
			if pos+suffixLen > suffixStart {
				continue
			}
			if !dataShareKeysEqualHashed(keys, keyHash, pos, suffixStart, suffixLen) {
				continue
			}
			if !dataShareReplayWindowSafe(messages[suffixStart:]) {
				return messages
			}
			// 尾部窗口完整重复早前历史且带强 replay 信号时，删除尾部污染副本，保留更早的真实上下文。
			return cloneBufferedDataShareMaps(messages[:suffixStart])
		}
	}
	return messages
}

func dataShareKeysEqual(keys []string, left int, right int, length int) bool {
	if left < 0 || right < 0 || length <= 0 || left+length > len(keys) || right+length > len(keys) {
		return false
	}
	for i := 0; i < length; i++ {
		if keys[left+i] == "" || keys[left+i] != keys[right+i] {
			return false
		}
	}
	return true
}

type dataShareReplayRangeHash struct {
	prefix []uint64
	power  []uint64
}

func dataShareNewReplayRangeHash(keys []string) dataShareReplayRangeHash {
	h := dataShareReplayRangeHash{
		prefix: []uint64{0},
		power:  []uint64{1},
	}
	for _, key := range keys {
		h.append(key)
	}
	return h
}

func (h *dataShareReplayRangeHash) append(key string) {
	value := dataShareReplayKeyHash(key)
	h.prefix = append(h.prefix, h.prefix[len(h.prefix)-1]*dataShareReplayRangeHashBase+value)
	h.power = append(h.power, h.power[len(h.power)-1]*dataShareReplayRangeHashBase)
}

func (h dataShareReplayRangeHash) rangeHash(start int, length int) (uint64, bool) {
	if start < 0 || length < 0 || start+length >= len(h.prefix) || length >= len(h.power) {
		return 0, false
	}
	return h.prefix[start+length] - h.prefix[start]*h.power[length], true
}

func dataShareReplayKeyHash(key string) uint64 {
	hash := uint64(1469598103934665603)
	for i := 0; i < len(key); i++ {
		hash ^= uint64(key[i])
		hash *= 1099511628211
	}
	if hash == 0 {
		return 1
	}
	return hash
}

func dataShareKeysEqualHashed(keys []string, keyHash dataShareReplayRangeHash, left int, right int, length int) bool {
	leftHash, ok := keyHash.rangeHash(left, length)
	if !ok {
		return false
	}
	rightHash, ok := keyHash.rangeHash(right, length)
	if !ok || leftHash != rightHash {
		return false
	}
	return dataShareKeysEqual(keys, left, right, length)
}

func dataShareCrossKeysEqual(leftKeys []string, leftStart int, rightKeys []string, rightStart int, length int) bool {
	if leftStart < 0 || rightStart < 0 || length <= 0 || leftStart+length > len(leftKeys) || rightStart+length > len(rightKeys) {
		return false
	}
	for i := 0; i < length; i++ {
		if leftKeys[leftStart+i] == "" || leftKeys[leftStart+i] != rightKeys[rightStart+i] {
			return false
		}
	}
	return true
}

func dataShareContiguousKeyMatchLenHashed(leftKeys []string, leftHash dataShareReplayRangeHash, leftStart int, rightKeys []string, rightHash dataShareReplayRangeHash, rightStart int) int {
	limit := len(leftKeys) - leftStart
	if remaining := len(rightKeys) - rightStart; remaining < limit {
		limit = remaining
	}
	if limit <= 0 || leftStart < 0 || rightStart < 0 {
		return 0
	}
	low, high := 0, limit
	for low < high {
		mid := (low + high + 1) / 2
		leftValue, leftOK := leftHash.rangeHash(leftStart, mid)
		rightValue, rightOK := rightHash.rangeHash(rightStart, mid)
		if leftOK && rightOK && leftValue == rightValue {
			low = mid
			continue
		}
		high = mid - 1
	}
	// hash 只做候选长度过滤；调用方在删除或报错前必须再做真实 identity 连续比较。
	return low
}

func dataShareHasReplayDuplicateBlock(messages []map[string]any) bool {
	if len(messages) < dataShareLongReplayMinMessages*2 {
		return false
	}
	keys := dataShareMessageIdentityKeys(messages)
	return dataShareHasUnsafeReplayDuplicateBlock(messages, keys)
}

func dataShareCompactGlobalReplayWindows(messages []map[string]any) []map[string]any {
	if len(messages) < dataShareLongReplayMinMessages*2 {
		return messages
	}
	// 非相邻窗口只在强信号下删除，避免把用户真实重复执行的一段长任务误判为 replay。
	keys := dataShareMessageIdentityKeys(messages)
	keyHash := dataShareNewReplayRangeHash(keys)
	safePrefix := dataShareReplaySafePrefix(messages)
	out := make([]map[string]any, 0, len(messages))
	outKeys := make([]string, 0, len(messages))
	outKeyHash := dataShareNewReplayRangeHash(nil)
	index := map[string][]int{}
	for i := 0; i < len(messages); {
		if len(outKeys) >= dataShareLongReplayMinMessages && len(keys)-i >= dataShareLongReplayMinMessages {
			windowKey := dataShareReplayWindowKey(keys, i)
			candidates := index[windowKey]
			if len(candidates) > 0 && len(candidates) <= dataShareReplayWindowCandidateLimit {
				best := 0
				bestPos := 0
				for _, pos := range candidates {
					length := dataShareContiguousKeyMatchLenHashed(outKeys, outKeyHash, pos, keys, keyHash, i)
					if length > best {
						best = length
						bestPos = pos
					}
				}
				if best >= dataShareLongReplayMinMessages &&
					dataShareReplayWindowSafeRange(safePrefix, i, i+best) &&
					dataShareCrossKeysEqual(outKeys, bestPos, keys, i, best) {
					i += best
					continue
				}
			}
		}
		out = append(out, cloneDataShareMap(messages[i]))
		outKeys = append(outKeys, keys[i])
		outKeyHash.append(keys[i])
		dataShareAddReplayWindowIndex(index, outKeys, len(outKeys)-1)
		i++
	}
	return out
}

func dataShareAddReplayWindowIndex(index map[string][]int, keys []string, appended int) {
	start := appended - dataShareReplayWindowWidth + 1
	if start < 0 {
		return
	}
	key := dataShareReplayWindowKey(keys, start)
	if key == "" {
		return
	}
	index[key] = append(index[key], start)
}

func dataShareReplayWindowSafe(messages []map[string]any) bool {
	for _, msg := range messages {
		if dataShareReplayMessageSafe(msg) {
			return true
		}
	}
	return false
}

func dataShareReplayMessageSafe(msg map[string]any) bool {
	if strings.TrimSpace(stringFromAny(msg["role"])) == "tool" || len(anySlice(msg["tool_calls"])) > 0 {
		return true
	}
	return dataShareSyntheticUserContextText(dataShareMessageTextForReplay(msg))
}

func dataShareReplaySafePrefix(messages []map[string]any) []int {
	prefix := make([]int, len(messages)+1)
	for i, msg := range messages {
		prefix[i+1] = prefix[i]
		if dataShareReplayMessageSafe(msg) {
			prefix[i+1]++
		}
	}
	return prefix
}

func dataShareReplayWindowSafeRange(prefix []int, start int, end int) bool {
	if start < 0 {
		start = 0
	}
	if end > len(prefix)-1 {
		end = len(prefix) - 1
	}
	if start >= end || len(prefix) == 0 {
		return false
	}
	return prefix[end] > prefix[start]
}

func dataShareHasUnsafeReplayDuplicateBlock(messages []map[string]any, keys []string) bool {
	if len(messages) < dataShareLongReplayMinMessages*2 || len(keys) != len(messages) {
		return false
	}
	keyHash := dataShareNewReplayRangeHash(keys)
	safePrefix := dataShareReplaySafePrefix(messages)
	index := dataShareReplayWindowIndex(keys)
	for start := 0; start+dataShareLongReplayMinMessages <= len(keys); start++ {
		candidates := index[dataShareReplayWindowKey(keys, start)]
		if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
			continue
		}
		for _, other := range candidates {
			if other <= start || other-start < dataShareLongReplayMinMessages {
				continue
			}
			length := dataShareContiguousKeyMatchLenHashed(keys, keyHash, start, keys, keyHash, other)
			if length < dataShareLongReplayMinMessages {
				continue
			}
			if dataShareReplayWindowSafeRange(safePrefix, start, start+length) ||
				dataShareReplayWindowSafeRange(safePrefix, other, other+length) {
				if dataShareKeysEqual(keys, start, other, length) {
					return true
				}
				continue
			}
			if other == start+length &&
				dataShareHasEarlierReplayBlock(keys, keyHash, index, start, length) &&
				dataShareKeysEqual(keys, start, other, length) {
				return true
			}
		}
	}
	return false
}

func appendDataShareQualityError(errs []string, code string) []string {
	code = strings.TrimSpace(code)
	if code == "" {
		return errs
	}
	for _, existing := range errs {
		if existing == code {
			return errs
		}
	}
	return append(errs, code)
}

func dataShareReplaySkipLenForMessages(existing, incoming []map[string]any, incomingStart int) int {
	if len(existing) == 0 || len(incoming) == 0 || incomingStart >= len(incoming) {
		return 0
	}
	existingIdentities := make([]string, len(existing))
	existingIdentityPositions := make(map[string][]int, len(existing))
	existingIdentityAt := func(index int) string {
		if existingIdentities[index] == "" {
			existingIdentities[index] = dataShareMessageIdentity(existing[index])
		}
		return existingIdentities[index]
	}
	for i := range existing {
		identity := existingIdentityAt(i)
		existingIdentityPositions[identity] = append(existingIdentityPositions[identity], i)
	}
	incomingIdentities := make([]string, len(incoming))
	incomingIdentityAt := func(index int) string {
		if incomingIdentities[index] == "" {
			incomingIdentities[index] = dataShareMessageIdentity(incoming[index])
		}
		return incomingIdentities[index]
	}
	seenToolCalls := map[string]struct{}{}
	seenToolResults := map[string]struct{}{}
	for _, msg := range existing {
		rememberDataShareMessage(msg, seenToolCalls, seenToolResults)
	}
	return dataShareReplaySkipLen(
		existing,
		len(incoming),
		incomingStart,
		existingIdentityAt,
		incomingIdentityAt,
		existingIdentityPositions,
		seenToolCalls,
		seenToolResults,
	)
}

func dataShareReplaySkipLen(
	existing []map[string]any,
	incomingLen int,
	incomingStart int,
	existingIdentityAt func(int) string,
	incomingIdentityAt func(int) string,
	existingIdentityPositions map[string][]int,
	seenToolCalls map[string]struct{},
	seenToolResults map[string]struct{},
) int {
	if len(existing) == 0 || incomingStart >= incomingLen {
		return 0
	}
	prefix := dataShareCommonPrefixLen(len(existing), existingIdentityAt, incomingStart, incomingLen, incomingIdentityAt)
	if prefix < dataShareReplayOverlapMinMessages {
		return 0
	}
	ordered := dataShareOrderedReplaySkipLen(existing, incomingLen, incomingStart, existingIdentityAt, incomingIdentityAt, existingIdentityPositions)
	if dataShareReplayPrefixSafe(existing[:prefix], prefix, seenToolCalls, seenToolResults) {
		if ordered > prefix {
			return ordered
		}
		return prefix
	}
	if dataShareOrderedReplayPrefixSafe(prefix, ordered, incomingStart, incomingIdentityAt) {
		return ordered
	}
	return 0
}

func dataShareReplayPrefixSafe(messages []map[string]any, prefix int, seenToolCalls map[string]struct{}, seenToolResults map[string]struct{}) bool {
	if prefix >= 5 {
		if prefix >= dataShareLongReplayMinMessages && !dataShareReplayWindowSafe(messages[:prefix]) {
			return false
		}
		return true
	}
	if prefix >= dataShareReplayOverlapMinMessages {
		role := strings.TrimSpace(stringFromAny(messages[0]["role"]))
		if role == "system" || role == "developer" {
			if prefix >= 3 {
				return true
			}
			return len(messages) > 1 && dataShareStrongSyntheticUserContextText(dataShareMessageTextForReplay(messages[1]))
		}
		if dataShareReplayPrefixStartsWithSyntheticUserContext(messages[:prefix]) {
			return true
		}
		if prefix >= 3 && dataShareReplayPrefixHasSyntheticContext(messages[:prefix]) {
			return true
		}
		return dataShareReplayPrefixHasSeenToolEcho(messages[:prefix], seenToolCalls, seenToolResults)
	}
	return false
}

func dataShareReplayPrefixStartsWithSyntheticUserContext(messages []map[string]any) bool {
	if len(messages) < dataShareReplayOverlapMinMessages {
		return false
	}
	if strings.TrimSpace(stringFromAny(messages[0]["role"])) != "user" {
		return false
	}
	first := dataShareMessageTextForReplay(messages[0])
	if !dataShareSyntheticUserContextText(first) {
		return false
	}
	secondRole := strings.TrimSpace(stringFromAny(messages[1]["role"]))
	if secondRole == "system" || secondRole == "developer" {
		return true
	}
	return dataShareSyntheticUserContextText(dataShareMessageTextForReplay(messages[1]))
}

func dataShareReplayPrefixHasSyntheticContext(messages []map[string]any) bool {
	for _, msg := range messages {
		if dataShareSyntheticUserContextText(dataShareMessageTextForReplay(msg)) {
			return true
		}
	}
	return false
}

func dataShareMessageTextForReplay(msg map[string]any) string {
	return strings.ToLower(dataShareContentText(firstPresentAny(msg["content"], msg["text"])))
}

func dataShareSyntheticUserContextText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if dataShareStrongSyntheticUserContextText(text) {
		return true
	}
	weakMatches := 0
	for _, marker := range []string{
		"agents.md instructions",
		"current_date",
		"filesystem sandboxing",
		"<cwd>",
		"<shell>",
	} {
		if strings.Contains(text, marker) {
			weakMatches++
		}
	}
	return weakMatches >= 2
}

func dataShareStrongSyntheticUserContextText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.HasPrefix(text, "<system-reminder") || strings.HasPrefix(text, "<system_reminder") {
		return true
	}
	for _, marker := range []string{
		"<command-message>",
		"<environment_context>",
		"<permissions instructions>",
		"base directory for this skill",
		"mcp server instructions",
		"deferred tools are now available",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	if strings.Contains(text, "subagent context") && strings.Contains(text, "you are running as a subagent") {
		return true
	}
	if strings.Contains(text, "[important:") && strings.Contains(text, "the user has invoked the") && strings.Contains(text, "skill") {
		return true
	}
	if strings.Contains(text, "todo list is currently empty") && strings.Contains(text, "do not mention this to the user") {
		return true
	}
	return false
}

func dataShareOrderedReplaySkipLen(existing []map[string]any, incomingLen int, incomingStart int, existingIdentityAt func(int) string, incomingIdentityAt func(int) string, existingIdentityPositions map[string][]int) int {
	if len(existing) == 0 || incomingStart >= incomingLen {
		return 0
	}
	incomingIndex := incomingStart
	existingCursor := 0
	var lastIncomingIndex int
	matched := 0
	for incomingIndex < incomingLen {
		position := dataShareNextIdentityPosition(existingIdentityPositions[incomingIdentityAt(incomingIndex)], existingCursor)
		if position < 0 {
			break
		}
		lastIncomingIndex = incomingIndex
		matched++
		incomingIndex++
		existingCursor = position + 1
	}
	if matched < dataShareReplayOverlapMinMessages {
		return 0
	}
	end := incomingIndex
	if dataShareShouldKeepPotentialReplayTailUser(incomingLen, incomingIndex, lastIncomingIndex, incomingIdentityAt) {
		end = lastIncomingIndex
	}
	if end <= incomingStart {
		return 0
	}
	return end - incomingStart
}

func dataShareOrderedReplayPrefixSafe(prefix int, ordered int, incomingStart int, incomingIdentityAt func(int) string) bool {
	if prefix < dataShareReplayOverlapMinMessages || ordered < 5 {
		return false
	}
	firstRole := dataShareIdentityRole(incomingIdentityAt(incomingStart))
	if firstRole != "system" && firstRole != "developer" {
		return false
	}
	for i := incomingStart; i < incomingStart+ordered; i++ {
		switch dataShareIdentityRole(incomingIdentityAt(i)) {
		case "assistant", "tool":
			return true
		}
	}
	return false
}

func dataShareNextIdentityPosition(positions []int, cursor int) int {
	if len(positions) == 0 {
		return -1
	}
	index := sort.SearchInts(positions, cursor)
	if index >= len(positions) {
		return -1
	}
	return positions[index]
}

func dataShareShouldKeepPotentialReplayTailUser(incomingLen int, incomingIndex int, lastIncomingIndex int, incomingIdentityAt func(int) string) bool {
	if lastIncomingIndex < 0 || dataShareIdentityRole(incomingIdentityAt(lastIncomingIndex)) != "user" {
		return false
	}
	if incomingIndex >= incomingLen {
		return true
	}
	return dataShareIdentityRole(incomingIdentityAt(incomingIndex)) == "assistant" || dataShareIdentityRole(incomingIdentityAt(incomingIndex)) == "tool"
}

func dataShareIdentityRole(identity string) string {
	if strings.HasPrefix(identity, "assistant_tool_calls:") {
		return "assistant"
	}
	if before, _, ok := strings.Cut(identity, ":"); ok {
		return before
	}
	return ""
}

// dataShareReplayPrefixHasSeenToolEcho 用已出现过的工具调用/result id 作为无边界 compact 的强 replay 信号，避免误删普通文本重复。
func dataShareReplayPrefixHasSeenToolEcho(messages []map[string]any, seenToolCalls map[string]struct{}, seenToolResults map[string]struct{}) bool {
	for _, msg := range messages {
		if strings.TrimSpace(stringFromAny(msg["role"])) == "tool" {
			if id := strings.TrimSpace(stringFromAny(msg["tool_call_id"])); id != "" {
				if _, ok := seenToolResults[id]; ok {
					return true
				}
			}
			continue
		}
		for _, id := range dataShareToolCallIDs(msg) {
			if _, ok := seenToolCalls[id]; ok {
				return true
			}
		}
	}
	return false
}

// dataShareMessagesAreExistingPrefix 判断 incoming 是否只是已聚合快照的前缀重放。
func dataShareMessagesAreExistingPrefix(existing, incoming []map[string]any) bool {
	if len(existing) == 0 || len(incoming) < dataShareReplayOverlapMinMessages || len(incoming) > len(existing) {
		return false
	}
	existingIdentities := dataShareMessageIdentities(existing)
	incomingIdentities := dataShareMessageIdentities(incoming)
	for i := range incoming {
		if existingIdentities[i] != incomingIdentities[i] {
			return false
		}
	}
	return true
}

func dataShareMessageIdentities(messages []map[string]any) []string {
	out := make([]string, len(messages))
	for i, msg := range messages {
		out[i] = dataShareMessageIdentity(msg)
	}
	return out
}

func dataShareMessageIdentityKeys(messages []map[string]any) []string {
	out := make([]string, len(messages))
	for i, msg := range messages {
		out[i] = dataShareResponsesIdentityKey(dataShareMessageIdentity(msg))
	}
	return out
}

type dataShareReplayMatch struct {
	existingStart int
	incomingStart int
	length        int
}

func dataShareBestIndexedReplayMatch(existingKeys []string, index map[string][]int, incomingKeys []string, incomingStart int) dataShareReplayMatch {
	if len(existingKeys) < dataShareLongReplayMinMessages || len(incomingKeys)-incomingStart < dataShareLongReplayMinMessages {
		return dataShareReplayMatch{}
	}
	// 使用三元组 hash 锁定当前位置候选，再用完整 identity 连续比较确认，避免随 incoming 尾部反复扫描。
	if incomingStart < 0 {
		incomingStart = 0
	}
	best := dataShareReplayMatch{}
	candidates := index[dataShareReplayWindowKey(incomingKeys, incomingStart)]
	if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
		return dataShareReplayMatch{}
	}
	for _, pos := range candidates {
		length := dataShareContiguousKeyMatchLen(existingKeys, pos, incomingKeys, incomingStart)
		if length > best.length {
			best = dataShareReplayMatch{existingStart: pos, incomingStart: incomingStart, length: length}
		}
	}
	if best.length < dataShareLongReplayMinMessages {
		return dataShareReplayMatch{}
	}
	return best
}

func dataShareReplayWindowIndex(keys []string) map[string][]int {
	index := map[string][]int{}
	limit := len(keys) - dataShareReplayWindowWidth
	for i := 0; i <= limit; i++ {
		key := dataShareReplayWindowKey(keys, i)
		if key == "" {
			continue
		}
		index[key] = append(index[key], i)
	}
	return index
}

func dataShareReplayWindowKey(keys []string, start int) string {
	if start < 0 || start+dataShareReplayWindowWidth > len(keys) {
		return ""
	}
	for i := 0; i < dataShareReplayWindowWidth; i++ {
		if keys[start+i] == "" {
			return ""
		}
	}
	return strings.Join(keys[start:start+dataShareReplayWindowWidth], "\x00")
}

func dataShareContiguousKeyMatchLen(existingKeys []string, existingStart int, incomingKeys []string, incomingStart int) int {
	length := 0
	for existingStart+length < len(existingKeys) && incomingStart+length < len(incomingKeys) {
		if existingKeys[existingStart+length] == "" || existingKeys[existingStart+length] != incomingKeys[incomingStart+length] {
			break
		}
		length++
	}
	return length
}

func dataShareMessageAlreadySeen(msg map[string]any, seenToolCalls map[string]struct{}, seenToolResults map[string]struct{}) bool {
	if strings.TrimSpace(stringFromAny(msg["role"])) == "tool" {
		id := strings.TrimSpace(stringFromAny(msg["tool_call_id"]))
		if id == "" {
			return false
		}
		_, ok := seenToolResults[id]
		return ok
	}
	callIDs := dataShareToolCallIDs(msg)
	if len(callIDs) == 0 {
		return false
	}
	for _, id := range callIDs {
		if _, ok := seenToolCalls[id]; !ok {
			return false
		}
	}
	return true
}

func rememberDataShareMessage(msg map[string]any, seenToolCalls map[string]struct{}, seenToolResults map[string]struct{}) {
	if strings.TrimSpace(stringFromAny(msg["role"])) == "tool" {
		if id := strings.TrimSpace(stringFromAny(msg["tool_call_id"])); id != "" {
			seenToolResults[id] = struct{}{}
		}
		return
	}
	for _, id := range dataShareToolCallIDs(msg) {
		seenToolCalls[id] = struct{}{}
	}
}

func dataShareCommentaryEchoAlreadySeen(msg map[string]any, seenAssistantText map[string]int, currentEpoch int) bool {
	if strings.TrimSpace(stringFromAny(msg["phase"])) != "commentary" {
		return false
	}
	key := dataShareAssistantTextKey(msg)
	if key == "" {
		return false
	}
	seenEpoch, ok := seenAssistantText[key]
	return ok && seenEpoch == currentEpoch
}

func rememberDataShareAssistantTextMessage(msg map[string]any, seenAssistantText map[string]int, currentEpoch int) {
	key := dataShareAssistantTextKey(msg)
	if key == "" {
		return
	}
	seenAssistantText[key] = currentEpoch
}

func dataShareAssistantTextKey(msg map[string]any) string {
	if strings.TrimSpace(stringFromAny(msg["role"])) != "assistant" {
		return ""
	}
	if len(anySlice(msg["tool_calls"])) > 0 {
		return ""
	}
	contentValue := firstPresentAny(msg["content"], msg["text"])
	content := strings.TrimSpace(dataShareContentText(contentValue))
	if content == "" || !dataShareContentIdentityCanUseText(contentValue) {
		return ""
	}
	return content
}

func dataShareAssistantTextEchoWindowReset(msg map[string]any) bool {
	if strings.TrimSpace(stringFromAny(msg["role"])) != "user" {
		return false
	}
	content := strings.TrimSpace(dataShareContentText(firstPresentAny(msg["content"], msg["text"])))
	if content == "" {
		return false
	}
	// 真实用户新输入开启新的去重窗口；AGENTS/环境等合成上下文不应打断 Responses input 回放识别。
	return !dataShareSyntheticUserContextText(strings.ToLower(content))
}

func dataShareToolCallIDs(msg map[string]any) []string {
	calls := anySlice(msg["tool_calls"])
	out := make([]string, 0, len(calls))
	for _, raw := range calls {
		call, ok := mapFromAny(raw)
		if !ok {
			continue
		}
		if id := strings.TrimSpace(stringFromAny(call["id"])); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// dataShareMessageIdentity 生成稳定身份，忽略 Responses message 的 id/status/type 等易变字段。
func dataShareMessageIdentity(msg map[string]any) string {
	role := strings.TrimSpace(stringFromAny(msg["role"]))
	if role == "" {
		return string(mustJSON(msg))
	}
	if role == "tool" {
		if id := strings.TrimSpace(stringFromAny(msg["tool_call_id"])); id != "" {
			return "tool:" + id
		}
	}
	if role == "assistant" {
		if calls := anySlice(msg["tool_calls"]); len(calls) > 0 {
			return "assistant_tool_calls:" + dataShareToolCallsIdentity(calls)
		}
	}
	contentValue := firstPresentAny(msg["content"], msg["text"])
	content := strings.TrimSpace(dataShareContentText(contentValue))
	if content != "" && dataShareContentIdentityCanUseText(contentValue) {
		return role + ":content:" + content
	}
	return role + ":structured:" + string(mustJSON(dataShareMessageIdentityPayload(msg, role)))
}

// dataShareMessageIdentityPayload 只清理 Responses 外层易变字段；其它字段可能承载 reasoning/refusal 等语义，必须参与身份。
func dataShareMessageIdentityPayload(msg map[string]any, role string) map[string]any {
	out := cloneDataShareMap(msg)
	delete(out, "id")
	delete(out, "status")
	delete(out, "type")
	if role != "" {
		out["role"] = role
	}
	if contentValue, ok := out["content"]; ok {
		out["content"] = normalizeDataShareContentValue(contentValue)
	}
	if textValue, ok := out["text"]; ok {
		out["text"] = normalizeDataShareContentValue(textValue)
	}
	return out
}

func dataShareContentIdentityCanUseText(value any) bool {
	switch v := value.(type) {
	case nil, string:
		return true
	case []any:
		if len(v) == 0 {
			return true
		}
		for _, item := range v {
			block, ok := mapFromAny(item)
			if !ok {
				return false
			}
			if !dataShareContentBlockIdentityCanUseText(block) {
				return false
			}
		}
		return true
	case []map[string]any:
		if len(v) == 0 {
			return true
		}
		for _, block := range v {
			if !dataShareContentBlockIdentityCanUseText(block) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func dataShareContentBlockIdentityCanUseText(block map[string]any) bool {
	blockType := strings.TrimSpace(stringFromAny(block["type"]))
	if blockType != "" {
		switch blockType {
		case "input_text", "output_text", "text":
		default:
			return false
		}
	}
	for key := range block {
		switch key {
		case "type", "text", "content":
			continue
		default:
			return false
		}
	}
	return true
}

// dataShareToolCallsIdentity 使用工具调用的业务字段生成身份，避免上游包装字段影响重放识别。
func dataShareToolCallsIdentity(calls []any) string {
	normalized := make([]map[string]any, 0, len(calls))
	for _, raw := range calls {
		call, ok := mapFromAny(raw)
		if !ok {
			normalized = append(normalized, map[string]any{"raw": raw})
			continue
		}
		functionMap, _ := mapFromAny(call["function"])
		normalized = append(normalized, map[string]any{
			"id":        firstNonBlank(stringFromAny(call["id"]), stringFromAny(call["call_id"]), stringFromAny(call["tool_call_id"])),
			"name":      firstNonBlank(stringFromAny(call["name"]), stringFromAny(functionMap["name"]), stringFromAny(call["type"])),
			"arguments": normalizeToolArguments(firstPresentAny(call["arguments"], functionMap["arguments"], call["input"])),
		})
	}
	return string(mustJSON(normalized))
}

func dataShareCommonPrefixLen(leftLen int, leftIdentityAt func(int) string, rightStart int, rightLen int, rightIdentityAt func(int) string) int {
	limit := leftLen
	if remaining := rightLen - rightStart; remaining < limit {
		limit = remaining
	}
	for i := 0; i < limit; i++ {
		if leftIdentityAt(i) != rightIdentityAt(rightStart+i) {
			return i
		}
	}
	return limit
}

func normalizeDataShareMessage(msg map[string]any) map[string]any {
	if msg == nil {
		return nil
	}
	msgType := strings.TrimSpace(stringFromAny(msg["type"]))
	switch msgType {
	case "function_call":
		return normalizeResponsesFunctionCallMessage(msg)
	case "function_call_output":
		return normalizeToolResultMessage(msg)
	}
	out := cloneDataShareMap(msg)
	role := normalizeResponsesInputRole(stringFromAny(out["role"]), msgType)
	if role != "" {
		out["role"] = role
	}
	if role == "tool" {
		return normalizeToolResultMessage(out)
	}
	if role == "assistant" {
		if calls := normalizeToolCalls(out["tool_calls"]); len(calls) > 0 {
			out["tool_calls"] = calls
			if _, ok := out["finish_reason"]; !ok {
				out["finish_reason"] = "tool_calls"
			}
		} else {
			delete(out, "tool_calls")
		}
	}
	if content, ok := out["content"]; ok {
		out["content"] = normalizeDataShareContentValue(content)
	} else if text := strings.TrimSpace(stringFromAny(out["text"])); text != "" {
		out["content"] = text
	}
	delete(out, "type")
	return out
}

func normalizeResponsesFunctionCallMessage(msg map[string]any) map[string]any {
	functionMap, _ := mapFromAny(msg["function"])
	call := map[string]any{
		"id":        firstNonBlank(stringFromAny(msg["call_id"]), stringFromAny(msg["id"]), stringFromAny(msg["tool_call_id"])),
		"name":      firstNonBlank(stringFromAny(msg["name"]), stringFromAny(functionMap["name"])),
		"arguments": normalizeToolArguments(firstPresentAny(msg["arguments"], functionMap["arguments"], msg["input"])),
	}
	return map[string]any{
		"role":          "assistant",
		"content":       normalizeDataShareContentValue(msg["content"]),
		"tool_calls":    []map[string]any{call},
		"finish_reason": "tool_calls",
	}
}

func normalizeToolResultMessage(msg map[string]any) map[string]any {
	callID := firstNonBlank(
		stringFromAny(msg["tool_call_id"]),
		stringFromAny(msg["call_id"]),
		stringFromAny(msg["tool_use_id"]),
		stringFromAny(msg["id"]),
	)
	content := normalizeDataShareContentValue(firstPresentAny(msg["content"], msg["output"], msg["result"], msg["error"]))
	isError := boolFromAny(msg["is_error"]) || dataShareStatusIsError(stringFromAny(msg["status"])) || dataShareToolContentLooksError(content) || msg["error"] != nil
	status := strings.TrimSpace(stringFromAny(msg["status"]))
	if status == "" {
		if isError {
			status = "error"
		} else {
			status = "success"
		}
	}
	out := map[string]any{
		"role":         "tool",
		"tool_call_id": callID,
		"content":      content,
		"status":       status,
		"is_error":     isError,
	}
	if errMsg := strings.TrimSpace(stringFromAny(msg["error_message"])); errMsg != "" {
		out["error_message"] = errMsg
	}
	return out
}

func normalizeToolCalls(value any) []map[string]any {
	rawCalls := anySlice(value)
	out := make([]map[string]any, 0, len(rawCalls))
	for _, raw := range rawCalls {
		call, ok := mapFromAny(raw)
		if !ok {
			continue
		}
		functionMap, _ := mapFromAny(call["function"])
		arguments := firstPresentAny(call["arguments"], functionMap["arguments"], call["input"])
		if arguments == nil && len(functionMap) > 0 {
			arguments = functionMap
		}
		out = append(out, map[string]any{
			"id":        firstNonBlank(stringFromAny(call["id"]), stringFromAny(call["call_id"]), stringFromAny(call["tool_call_id"])),
			"name":      firstNonBlank(stringFromAny(call["name"]), stringFromAny(functionMap["name"]), stringFromAny(call["type"])),
			"arguments": normalizeToolArguments(arguments),
		})
	}
	return out
}

func normalizeToolArguments(value any) any {
	value = firstPresentAny(value)
	if value == nil {
		return map[string]any{}
	}
	if raw, ok := value.(string); ok {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return map[string]any{}
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			return parsed
		}
		return raw
	}
	return value
}
