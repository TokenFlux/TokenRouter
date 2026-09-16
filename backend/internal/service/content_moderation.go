// 旧审核入口仅转接唯一 moderation 实现，S15/S16 清理。
package service

import (
	context "context"
	"database/sql"
	errors "errors"
	"fmt"
	time "time"

	"github.com/TokenFlux/TokenRouter/internal/moderation/provider"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	native "github.com/TokenFlux/TokenRouter/internal/moderation"
)

const ContentModerationModeOff = native.ContentModerationModeOff
const ContentModerationModeObserve = native.ContentModerationModeObserve
const ContentModerationModePreBlock = native.ContentModerationModePreBlock

const ContentModerationActionAllow = native.ContentModerationActionAllow
const ContentModerationActionBlock = native.ContentModerationActionBlock
const ContentModerationActionHashBlock = native.ContentModerationActionHashBlock
const ContentModerationActionKeywordBlock = native.ContentModerationActionKeywordBlock
const ContentModerationActionError = native.ContentModerationActionError

const ContentModerationKeywordModeKeywordOnly = native.ContentModerationKeywordModeKeywordOnly
const ContentModerationKeywordModeKeywordAndAPI = native.ContentModerationKeywordModeKeywordAndAPI
const ContentModerationKeywordModeAPIOnly = native.ContentModerationKeywordModeAPIOnly
const ContentModerationModelFilterAll = native.ContentModerationModelFilterAll
const ContentModerationModelFilterInclude = native.ContentModerationModelFilterInclude
const ContentModerationModelFilterExclude = native.ContentModerationModelFilterExclude
const ContentModerationProtocolAnthropicMessages = native.ContentModerationProtocolAnthropicMessages
const ContentModerationProtocolOpenAIResponses = native.ContentModerationProtocolOpenAIResponses
const ContentModerationProtocolOpenAIChat = native.ContentModerationProtocolOpenAIChat
const ContentModerationProtocolGemini = native.ContentModerationProtocolGemini
const ContentModerationProtocolOpenAIImages = native.ContentModerationProtocolOpenAIImages

func ContentModerationDefaultThresholds() map[string]float64 {
	return native.ContentModerationDefaultThresholds()
}
func ContentModerationCategories() []string { return native.ContentModerationCategories() }

type ContentModerationConfig = native.ContentModerationConfig
type ContentModerationConfigView = native.ContentModerationConfigView
type ContentModerationAPIKeyMetadata = native.ContentModerationAPIKeyMetadata
type ContentModerationAPIKeyEntryInput = native.ContentModerationAPIKeyEntryInput
type ContentModerationAPIKeyStatus = native.ContentModerationAPIKeyStatus
type ContentModerationAPIKeyLoad = native.ContentModerationAPIKeyLoad
type TestContentModerationAPIKeysInput = native.TestContentModerationAPIKeysInput
type TestContentModerationAPIKeysResult = native.TestContentModerationAPIKeysResult
type ContentModerationTestAuditResult = native.ContentModerationTestAuditResult
type UpdateContentModerationConfigInput = native.UpdateContentModerationConfigInput
type ContentModerationModelFilter = native.ContentModerationModelFilter
type ContentModerationCheckInput = native.ContentModerationCheckInput

const ContentModerationSourceUser = native.ContentModerationSourceUser
const ContentModerationSourceTool = native.ContentModerationSourceTool
const ContentModerationSourceMixed = native.ContentModerationSourceMixed
const ContentModerationItemTypeText = native.ContentModerationItemTypeText
const ContentModerationItemTypeImage = native.ContentModerationItemTypeImage
const ContentModerationItemTypeRequest = native.ContentModerationItemTypeRequest

type ContentModerationInputItem = native.ContentModerationInputItem
type ContentModerationImage = native.ContentModerationImage
type ContentModerationInput = native.ContentModerationInput

type ContentModerationDecision = native.ContentModerationDecision
type ContentModerationFailedUnit = native.ContentModerationFailedUnit
type ContentModerationMedia = native.ContentModerationMedia
type ContentModerationLog = native.ContentModerationLog
type ContentModerationCyberWarning = native.ContentModerationCyberWarning
type ContentModerationCyberWarningPolicy = native.ContentModerationCyberWarningPolicy
type ContentModerationCyberWarningInput = native.ContentModerationCyberWarningInput
type ContentModerationLogFilter = native.ContentModerationLogFilter
type ContentModerationCyberWarningFilter = native.ContentModerationCyberWarningFilter
type ContentModerationCyberSummary = native.ContentModerationCyberSummary
type ContentModerationCyberUserSummary = native.ContentModerationCyberUserSummary
type ContentModerationCyberAccountSummary = native.ContentModerationCyberAccountSummary
type ContentModerationCleanupResult = native.ContentModerationCleanupResult
type ContentModerationRuntimeStatus = native.ContentModerationRuntimeStatus
type ContentModerationUnbanUserResult = native.ContentModerationUnbanUserResult
type ContentModerationDeleteHashResult = native.ContentModerationDeleteHashResult
type ContentModerationClearHashesResult = native.ContentModerationClearHashesResult
type ContentModerationRepository = native.ContentModerationRepository
type ContentModerationReviewRepository = native.ContentModerationReviewRepository
type ContentModerationHashCache = native.ContentModerationHashCache

func defaultContentModerationConfig() *ContentModerationConfig {
	return native.LegacyDefaultContentModerationConfig()
}

// ContentModerationService 保留旧构造签名，生产 app 将直接注入核心。
type ContentModerationService struct {
	*native.ContentModerationService
}

func WrapContentModeration(core *native.ContentModerationService) *ContentModerationService {
	return &ContentModerationService{ContentModerationService: core}
}
func NewContentModerationService(settings SettingRepository, repo ContentModerationRepository, hash ContentModerationHashCache, groups GroupRepository, users UserRepository, auth APIKeyAuthCacheInvalidator, email *EmailService) *ContentModerationService {
	var groupPort native.GroupRepository
	if groups != nil {
		groupPort = legacyModerationGroups{groups}
	}
	var userPort native.UserCommands
	if users != nil {
		userPort = legacyModerationUsers{users}
	}
	var mail native.RiskSender
	if email != nil {
		mail = notification.NewRiskDelivery(email.Mailer)
	}
	runtime := native.Runtime{Audit: provider.NewAuditClient(), SnapshotMedia: provider.SnapshotMedia, Background: func(name string, fn func()) { RunBackgroundTask(name, BackgroundCall0(fn)) }, CyberText: nativeopenai.IsOpenAICyberWarningText, CyberPolicy: nativeopenai.DetectOpenAICyberPolicy, ErrorMessage: extractUpstreamErrorMessage, MissingRow: func(e error) bool { return errors.Is(e, sql.ErrNoRows) }, MissingUser: func(e error) bool { return errors.Is(e, ErrUserNotFound) }}
	return WrapContentModeration(native.NewContentModerationService(settings, repo, hash, groupPort, userPort, auth, mail, runtime))
}

type legacyModerationGroups struct{ source GroupRepository }

func (p legacyModerationGroups) CheckGroup(ctx context.Context, id int64) error {
	_, e := p.source.GetByIDLite(ctx, id)
	return e
}

type legacyModerationUsers struct{ source UserRepository }

func (p legacyModerationUsers) GetByID(ctx context.Context, id int64) (*native.UserSnapshot, error) {
	u, e := p.source.GetByID(ctx, id)
	if u == nil {
		return nil, e
	}
	return &native.UserSnapshot{ID: u.ID, Role: u.Role, Status: u.Status}, e
}
func (p legacyModerationUsers) SetStatus(ctx context.Context, id int64, status string) error {
	return p.source.Update(ctx, &User{ID: id, Status: status}, UserUpdateFields{Status: true})
}

type legacyModerationProxies struct{ source ProxyRepository }

func (p legacyModerationProxies) Lookup(ctx context.Context, id int64, now time.Time) (native.ProxyInfo, error) {
	v, e := p.source.GetByID(ctx, id)
	if e != nil {
		return native.ProxyInfo{}, e
	}
	return native.ProxyInfo{URL: v.URL(), Name: v.Name, Status: v.Status, Address: fmt.Sprintf("%s://%s:%d", v.Protocol, v.Host, v.Port), Active: v.IsActive(), Expired: v.IsExpired(now)}, nil
}
func (s *ContentModerationService) SetProxyRepository(p ProxyRepository) {
	if p == nil {
		s.ContentModerationService.SetProxyRepository(nil)
	} else {
		s.ContentModerationService.SetProxyRepository(legacyModerationProxies{p})
	}
}
func IsOpenAICyberWarningText(text string) bool { return nativeopenai.IsOpenAICyberWarningText(text) }

func extractCyberWarningText(body []byte) string { return native.ExtractCyberWarningText(body) }

func (s *ContentModerationService) buildLog(input ContentModerationCheckInput, cfg *ContentModerationConfig, action string, flagged bool, highestCategory string, highestScore float64, scores map[string]float64, text string, latency *int, queueDelay *int, errText string) *ContentModerationLog {
	core := s.ContentModerationService
	if core == nil {
		core = &native.ContentModerationService{}
	}
	return core.LegacyBuildLog(input, cfg, action, flagged, highestCategory, highestScore, scores, text, latency, queueDelay, errText)
}
