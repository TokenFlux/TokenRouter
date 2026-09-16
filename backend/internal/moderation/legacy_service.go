// Legacy 出口只服务未迁入口，算法和状态仍在本包，S15/S16 删除。
package moderation

import "time"

const LegacyContentModerationAPIKeysModeAppend = contentModerationAPIKeysModeAppend
const LegacyContentModerationAPIKeysModeReplace = contentModerationAPIKeysModeReplace
const LegacyContentModerationKeywordCategory = contentModerationKeywordCategory
const LegacyDefaultContentModerationBaseURL = defaultContentModerationBaseURL
const LegacyDefaultContentModerationModel = defaultContentModerationModel
const LegacyDefaultContentModerationTimeoutMS = defaultContentModerationTimeoutMS
const LegacyMaxContentModerationTimeoutMS = maxContentModerationTimeoutMS
const LegacyMaxModerationInputRunes = maxModerationInputRunes
const LegacyContentModerationChunkOverlap = contentModerationChunkOverlap
const LegacyContentModerationTextBatchSize = contentModerationTextBatchSize
const LegacyContentModerationImageConcurrency = contentModerationImageConcurrency
const LegacyContentModerationAuditTotalTimeout = contentModerationAuditTotalTimeout
const LegacyMaxModerationExcerptRunes = maxModerationExcerptRunes
const LegacyMaxCyberWarningPromptExcerptRunes = maxCyberWarningPromptExcerptRunes
const LegacyDefaultContentModerationWorkerCount = defaultContentModerationWorkerCount
const LegacyMaxContentModerationWorkerCount = maxContentModerationWorkerCount
const LegacyDefaultContentModerationQueueSize = defaultContentModerationQueueSize
const LegacyMaxContentModerationQueueSize = maxContentModerationQueueSize
const LegacyMaxContentModerationBufferedBytes = maxContentModerationBufferedBytes
const LegacyDefaultContentModerationBanThreshold = defaultContentModerationBanThreshold
const LegacyDefaultContentModerationViolationWindowHours = defaultContentModerationViolationWindowHours
const LegacyDefaultContentModerationBlockHTTPStatus = defaultContentModerationBlockHTTPStatus
const LegacyDefaultContentModerationBlockMessage = defaultContentModerationBlockMessage
const LegacyDefaultContentModerationRetryCount = defaultContentModerationRetryCount
const LegacyMaxContentModerationRetryCount = maxContentModerationRetryCount
const LegacyDefaultContentModerationHitRetentionDays = defaultContentModerationHitRetentionDays
const LegacyDefaultContentModerationNonHitRetentionDays = defaultContentModerationNonHitRetentionDays
const LegacyDefaultContentModerationCyberBanThreshold = defaultContentModerationCyberBanThreshold
const LegacyDefaultContentModerationCyberWindowHours = defaultContentModerationCyberWindowHours
const LegacyMaxContentModerationRetentionDays = maxContentModerationRetentionDays
const LegacyMaxContentModerationNonHitRetentionDays = maxContentModerationNonHitRetentionDays
const LegacyContentModerationKeyRateLimitFreezeDuration = contentModerationKeyRateLimitFreezeDuration
const LegacyContentModerationKeyAuthFreezeDuration = contentModerationKeyAuthFreezeDuration
const LegacyContentModerationKeyHTTPErrorFreezeDuration = contentModerationKeyHTTPErrorFreezeDuration
const LegacyMaxContentModerationTestImages = maxContentModerationTestImages
const LegacyMaxContentModerationTestImageBytes = maxContentModerationTestImageBytes
const LegacyMaxContentModerationTestImageDataURLBytes = maxContentModerationTestImageDataURLBytes
const LegacyMaxContentModerationBlockedKeywords = maxContentModerationBlockedKeywords
const LegacyMaxContentModerationBlockedKeywordRunes = maxContentModerationBlockedKeywordRunes
const LegacyMaxContentModerationModelFilterModels = maxContentModerationModelFilterModels
const LegacyMaxContentModerationModelFilterRunes = maxContentModerationModelFilterRunes
const LegacyMaxContentModerationAuditTextChars = maxContentModerationAuditTextChars
const LegacyDefaultContentModerationAuditTextChars = defaultContentModerationAuditTextChars
const LegacyDefaultContentModerationAPIKeyPriority = defaultContentModerationAPIKeyPriority
const LegacyMaxContentModerationAPIKeyPriority = maxContentModerationAPIKeyPriority
const LegacyMaxContentModerationAPIKeyNoteRunes = maxContentModerationAPIKeyNoteRunes
const LegacyContentModerationCleanupInterval = contentModerationCleanupInterval
const LegacyContentModerationCleanupTimeout = contentModerationCleanupTimeout
const LegacyContentModerationCleanupDelay = contentModerationCleanupDelay
const LegacyContentModerationRuntimeCacheTTL = contentModerationRuntimeCacheTTL
const LegacyContentModerationRuntimeRefreshTimeout = contentModerationRuntimeRefreshTimeout

var LegacyContentModerationCategoryOrder = contentModerationCategoryOrder

func LegacyNormalizeContentModerationSource(source string) string {
	return normalizeContentModerationSource(source)
}
func LegacyContentModerationInputSource(items []ContentModerationInputItem) string {
	return contentModerationInputSource(items)
}

type LegacyContentModerationRuntimeSnapshot = contentModerationRuntimeSnapshot
type LegacyContentModerationTask = contentModerationTask
type LegacyContentModerationKeyHealth = contentModerationKeyHealth
type LegacyContentModerationAuditResult = contentModerationAuditResult
type LegacyContentModerationUnitResult = contentModerationUnitResult
type LegacyContentModerationAPIError = contentModerationAPIError

func LegacyIsContentModerationBatchUnsupportedError(err error) bool {
	return isContentModerationBatchUnsupportedError(err)
}
func LegacyMergeContentModerationUnitResult(target *contentModerationAuditResult, unit contentModerationUnitResult, thresholds map[string]float64) {
	mergeContentModerationUnitResult(target, unit, thresholds)
}
func LegacyPublicContentModerationError(err error) string { return publicContentModerationError(err) }
func LegacySplitContentModerationText(text string, chunkSize int, overlap int) []string {
	return splitContentModerationText(text, chunkSize, overlap)
}
func LegacyNormalizeContentModerationChunkOptions(chunkSize int, overlap int) (int, int) {
	return normalizeContentModerationChunkOptions(chunkSize, overlap)
}
func LegacyCountContentModerationTextChunks(text string, chunkSize int, overlap int) int {
	return countContentModerationTextChunks(text, chunkSize, overlap)
}
func LegacyForEachContentModerationTextBatch(text string, chunkSize int, overlap int, batchSize int, visit func(startIndex int, chunks []string)) {
	forEachContentModerationTextBatch(text, chunkSize, overlap, batchSize, visit)
}
func LegacyContentModerationHitMedia(content ContentModerationInput, indexes []int) []ContentModerationMedia {
	return contentModerationHitMedia(content, indexes)
}
func LegacyCompactContentModerationInputForQueue(content ContentModerationInput) ContentModerationInput {
	return compactContentModerationInputForQueue(content)
}
func LegacyContentModerationTextFromItems(items []ContentModerationInputItem) string {
	return contentModerationTextFromItems(items)
}
func LegacyEstimateContentModerationTaskBytes(task contentModerationTask) int64 {
	return estimateContentModerationTaskBytes(task)
}
func LegacyParseContentModerationConfig(raw string) (*ContentModerationConfig, error) {
	return parseContentModerationConfig(raw)
}

type LegacyModerationProxyURLCacheEntry = moderationProxyURLCacheEntry

const LegacyContentModerationProxyURLCacheTTL = contentModerationProxyURLCacheTTL

func LegacyContentModerationEmailUserID(log *ContentModerationLog) int64 {
	return contentModerationEmailUserID(log)
}
func LegacyDefaultContentModerationConfig() *ContentModerationConfig {
	return defaultContentModerationConfig()
}
func LegacyCloneContentModerationConfig(cfg *ContentModerationConfig) *ContentModerationConfig {
	return cloneContentModerationConfig(cfg)
}
func LegacyContentModerationLogGroupID(groupID *int64) int64 {
	return contentModerationLogGroupID(groupID)
}

type LegacyContentModerationAPIKeyRuntimeEntry = contentModerationAPIKeyRuntimeEntry

func LegacyContentModerationFreezeDurationForHTTPStatus(httpStatus int) time.Duration {
	return contentModerationFreezeDurationForHTTPStatus(httpStatus)
}
func LegacyModerationAPIKeyHash(key string) string { return moderationAPIKeyHash(key) }
func LegacyBuildModerationTestInput(prompt string, images []string) (any, int, error) {
	return buildModerationTestInput(prompt, images)
}
func LegacyContentModerationTestHasAuditInput(prompt string, images []string) bool {
	return contentModerationTestHasAuditInput(prompt, images)
}
func LegacyValidateModerationTestImageDataURL(value string) error {
	return validateModerationTestImageDataURL(value)
}
func LegacyBuildContentModerationTestAuditResult(result *moderationAPIResult, thresholds map[string]float64) *ContentModerationTestAuditResult {
	return buildContentModerationTestAuditResult(result, thresholds)
}

type LegacyModerationAPIRequest = moderationAPIRequest
type LegacyModerationAPIInputPart = moderationAPIInputPart
type LegacyModerationAPIImageURLRef = moderationAPIImageURLRef
type LegacyModerationAPIResponse = moderationAPIResponse
type LegacyModerationAPIResult = moderationAPIResult

func LegacyEvaluateModerationScores(scores map[string]float64, thresholds map[string]float64) (bool, string, float64) {
	return evaluateModerationScores(scores, thresholds)
}
func LegacyMergeContentModerationThresholds(base map[string]float64, override map[string]float64) map[string]float64 {
	return mergeContentModerationThresholds(base, override)
}
func LegacyNormalizeInt64IDs(ids []int64) []int64         { return normalizeInt64IDs(ids) }
func LegacyNormalizeBlockedKeywords(in []string) []string { return normalizeBlockedKeywords(in) }
func LegacyNormalizeKeywordBlockingMode(mode string) string {
	return normalizeKeywordBlockingMode(mode)
}
func LegacyNormalizeContentModerationModelFilter(filter ContentModerationModelFilter) ContentModerationModelFilter {
	return normalizeContentModerationModelFilter(filter)
}
func LegacyCloneContentModerationModelFilter(filter ContentModerationModelFilter) ContentModerationModelFilter {
	return cloneContentModerationModelFilter(filter)
}
func LegacyNormalizeContentModerationModelFilterType(filterType string) string {
	return normalizeContentModerationModelFilterType(filterType)
}
func LegacyNormalizeContentModerationModelNames(models []string) []string {
	return normalizeContentModerationModelNames(models)
}
func LegacyContentModerationModelListContains(models []string, model string) bool {
	return contentModerationModelListContains(models, model)
}
func LegacyMatchBlockedKeyword(text string, keywords []string) (string, bool) {
	return matchBlockedKeyword(text, keywords)
}
func LegacyNormalizeModerationAPIKeys(keys []string) []string {
	return normalizeModerationAPIKeys(keys)
}
func LegacyNormalizeContentModerationAPIKeyPriority(priority int) int {
	return normalizeContentModerationAPIKeyPriority(priority)
}
func LegacyNormalizeContentModerationAPIKeyMetadata(keys []string, items []ContentModerationAPIKeyMetadata) []ContentModerationAPIKeyMetadata {
	return normalizeContentModerationAPIKeyMetadata(keys, items)
}
func LegacyNormalizeContentModerationAPIKeyEntryInputs(input *[]ContentModerationAPIKeyEntryInput) []ContentModerationAPIKeyEntryInput {
	return normalizeContentModerationAPIKeyEntryInputs(input)
}
func LegacyApplyContentModerationAPIKeyEntryMetadata(cfg *ContentModerationConfig, entries []ContentModerationAPIKeyEntryInput) {
	applyContentModerationAPIKeyEntryMetadata(cfg, entries)
}
func LegacyApplyContentModerationAPIKeyMetadataUpdates(cfg *ContentModerationConfig, updates []ContentModerationAPIKeyMetadata) error {
	return applyContentModerationAPIKeyMetadataUpdates(cfg, updates)
}
func LegacyValidateContentModerationAPIKeyInputs(entries *[]ContentModerationAPIKeyEntryInput, updates *[]ContentModerationAPIKeyMetadata) error {
	return validateContentModerationAPIKeyInputs(entries, updates)
}
func LegacyValidateContentModerationAuditTextLimit(limit *int, label string) error {
	return validateContentModerationAuditTextLimit(limit, label)
}
func LegacyDeleteModerationAPIKeysByHash(keys []string, hashes []string) []string {
	return deleteModerationAPIKeysByHash(keys, hashes)
}
func LegacyNormalizeContentModerationAPIKeysMode(mode string) string {
	return normalizeContentModerationAPIKeysMode(mode)
}
func LegacyNormalizeContentModerationHash(inputHash string) string {
	return normalizeContentModerationHash(inputHash)
}
func LegacyCloneFloatMap(in map[string]float64) map[string]float64 { return cloneFloatMap(in) }
func LegacyCloneInt64Ptr(in *int64) *int64                         { return cloneInt64Ptr(in) }
func LegacySanitizeContentModerationExcerpt(text string, max int) string {
	return sanitizeContentModerationExcerpt(text, max)
}
func LegacyTrimRawContentModerationText(text string, max int) string {
	return trimRawContentModerationText(text, max)
}
func LegacyTrimRunes(text string, max int) string { return trimRunes(text, max) }
func LegacyMaskSecretTail(secret string) string   { return maskSecretTail(secret) }

func (s *ContentModerationService) LegacyBuildLog(input ContentModerationCheckInput, cfg *ContentModerationConfig, action string, flagged bool, highestCategory string, highestScore float64, scores map[string]float64, text string, latency *int, queueDelay *int, errText string) *ContentModerationLog {
	return s.buildLog(input, cfg, action, flagged, highestCategory, highestScore, scores, text, latency, queueDelay, errText)
}
