package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
	"golang.org/x/sync/singleflight"
)

type qoderPATExchanger func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error)
type qoderCNPATExchanger func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error)
type qoderOrganizationTagsGetter func(ctx context.Context, token, uid string) (*qoder.OrganizationTags, error)

type qoderSessionCacheEntry struct {
	credentialsHash string
	session         *qoder.SessionContext
	expiresAt       time.Time
}

// qoderSessionAccountState 保存单个账号已观察到的最新凭据顺序与异步构建快照。
type qoderSessionAccountState struct {
	credentialsHash   string
	credentialVersion int64
	accountSnapshot   *Account
	generation        uint64
}

// qoderSessionBuildResult 保存 singleflight 共享的 session 构建结果。
type qoderSessionBuildResult struct {
	session *qoder.SessionContext
}

// defaultQoderSessionBuildTimeout 限制脱离单个请求生命周期后的共享凭据交换，避免异常上游永久占用 singleflight。
const defaultQoderSessionBuildTimeout = 90 * time.Second

// errQoderSessionBuildInvalidated 表示在途构建已被显式失效或新凭据取代。
var errQoderSessionBuildInvalidated = errors.New("qoder session invalidated during build")

// QoderTokenProvider 为 Qoder 账号构建并缓存 COSY session 上下文。
type QoderTokenProvider struct {
	mu                  sync.Mutex
	sessions            map[int64]qoderSessionCacheEntry
	accountStates       map[int64]qoderSessionAccountState
	sessionBuildGroup   singleflight.Group
	sessionBuildTimeout time.Duration
	exchangePAT         qoderPATExchanger
	exchangeCNPAT       qoderCNPATExchanger
	getOrgTags          qoderOrganizationTagsGetter
	httpUpstream        HTTPUpstream
	tlsFPProfileService *TLSFingerprintProfileService
}

func NewQoderTokenProvider() *QoderTokenProvider {
	return &QoderTokenProvider{
		sessions:            make(map[int64]qoderSessionCacheEntry),
		accountStates:       make(map[int64]qoderSessionAccountState),
		sessionBuildTimeout: defaultQoderSessionBuildTimeout,
	}
}

func (p *QoderTokenProvider) SetHTTPUpstream(httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) {
	if p == nil {
		return
	}
	p.httpUpstream = httpUpstream
	p.tlsFPProfileService = tlsFPProfileService
}

// @project-doc docs/interfaces/qoder_upstream.md#qoder_account_contract
func (p *QoderTokenProvider) GetSession(ctx context.Context, account *Account) (*qoder.SessionContext, error) {
	if p == nil {
		return nil, errors.New("qoder token provider is nil")
	}
	if account == nil {
		return nil, errors.New("account is nil")
	}
	if account.Platform != PlatformQoder || account.Type != AccountTypeCosy {
		return nil, errors.New("not a qoder cosy account")
	}

	accountSnapshot := snapshotQoderSessionAccount(account)
	hash := qoderCredentialsHash(accountSnapshot.Credentials)
	generation, hash, accountSnapshot, session := p.prepareQoderSessionBuild(accountSnapshot, hash)
	if session != nil {
		return session, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 凭据哈希与失效世代共同组成 key：相同请求只回源一次，失效或改密后不会等待旧交换。
	flightKey := fmt.Sprintf("%d:%s:%d", accountSnapshot.ID, hash, generation)
	resultCh := p.sessionBuildGroup.DoChan(flightKey, func() (any, error) {
		// 共享构建不能绑定首个等待者；每个调用者在下方独立响应自己的取消信号。
		buildTimeout := p.sessionBuildTimeout
		if buildTimeout <= 0 {
			buildTimeout = defaultQoderSessionBuildTimeout
		}
		buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), buildTimeout)
		defer cancel()

		if cached, current := p.cachedQoderSessionForBuild(accountSnapshot.ID, hash, generation); !current {
			return nil, errQoderSessionBuildInvalidated
		} else if cached != nil {
			return &qoderSessionBuildResult{session: cached}, nil
		}

		session, expiresAt, buildErr := p.buildSession(buildCtx, accountSnapshot)
		if buildErr != nil {
			return nil, buildErr
		}
		if !p.storeQoderSessionBuild(accountSnapshot.ID, hash, generation, session, expiresAt) {
			return nil, errQoderSessionBuildInvalidated
		}
		return &qoderSessionBuildResult{session: session}, nil
	})

	var value any
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case flightResult := <-resultCh:
		if flightResult.Err != nil {
			// 被失效或改密打断的旧调用不能返回已经失效的 session，由后续业务调用按新凭据重建。
			return nil, flightResult.Err
		}
		value = flightResult.Val
	}
	result, ok := value.(*qoderSessionBuildResult)
	if !ok || result == nil || result.session == nil {
		return nil, errors.New("qoder session build returned an invalid result")
	}
	return result.session, nil
}

// snapshotQoderSessionAccount 为异步共享构建复制所有会读取的可变账号数据。
func snapshotQoderSessionAccount(account *Account) *Account {
	snapshot := *account
	if account.Credentials != nil {
		snapshot.Credentials = shallowCopyMap(account.Credentials)
	}
	if account.Extra != nil {
		snapshot.Extra = shallowCopyMap(account.Extra)
	}
	if account.ProxyID != nil {
		proxyID := *account.ProxyID
		snapshot.ProxyID = &proxyID
	}
	if account.Proxy != nil {
		proxy := *account.Proxy
		snapshot.Proxy = &proxy
	}
	return &snapshot
}

// qoderCredentialSnapshotOlder 比较凭据快照顺序：明确的 token_version 优先，无法判序时才参考更新时间。
func qoderCredentialSnapshotOlder(incoming, current *Account) bool {
	if incoming == nil || current == nil {
		return false
	}
	incomingVersion := incoming.GetCredentialAsInt64("_token_version")
	currentVersion := current.GetCredentialAsInt64("_token_version")
	if incomingVersion > 0 && currentVersion > 0 && incomingVersion != currentVersion {
		return incomingVersion < currentVersion
	}
	if incomingVersion > 0 && currentVersion == 0 {
		return false
	}
	if incomingVersion == 0 && currentVersion > 0 {
		return incoming.UpdatedAt.IsZero() || current.UpdatedAt.IsZero() || !incoming.UpdatedAt.After(current.UpdatedAt)
	}
	return !incoming.UpdatedAt.IsZero() && !current.UpdatedAt.IsZero() && incoming.UpdatedAt.Before(current.UpdatedAt)
}

// prepareQoderSessionBuild 返回当前凭据状态；低版本调度快照只能复用已观察到的新凭据，不能反向淘汰它。
func (p *QoderTokenProvider) prepareQoderSessionBuild(account *Account, hash string) (uint64, string, *Account, *qoder.SessionContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	accountID := account.ID
	if p.sessions == nil {
		p.sessions = make(map[int64]qoderSessionCacheEntry)
	}
	if p.accountStates == nil {
		p.accountStates = make(map[int64]qoderSessionAccountState)
	}

	incomingVersion := account.GetCredentialAsInt64("_token_version")
	state := p.accountStates[accountID]
	if state.credentialsHash == "" {
		state.credentialsHash = hash
		state.credentialVersion = incomingVersion
		state.accountSnapshot = account
	} else if state.credentialsHash != hash {
		current := state.accountSnapshot
		if qoderCredentialSnapshotOlder(account, current) {
			// 刷新后的 token_version 单调递增；旧 scheduler 快照改用已观察到的新凭据构建或加入 flight。
			hash = state.credentialsHash
			account = current
		} else {
			state.generation++
			state.credentialsHash = hash
			state.credentialVersion = incomingVersion
			state.accountSnapshot = account
			delete(p.sessions, accountID)
		}
	} else {
		// 相同凭据下保留最新的代理、TLS 等非哈希运行时配置，供后续过期重建使用。
		state.accountSnapshot = account
	}
	p.accountStates[accountID] = state
	if entry, ok := p.sessions[accountID]; ok && entry.credentialsHash == hash && entry.session != nil && !qoderSessionCacheEntryExpired(entry, time.Now()) {
		return state.generation, hash, account, entry.session
	}
	delete(p.sessions, accountID)
	return state.generation, hash, account, nil
}

// cachedQoderSessionForBuild 在单飞回调内复查缓存与世代，避免排队期间重复回源。
func (p *QoderTokenProvider) cachedQoderSessionForBuild(accountID int64, hash string, generation uint64) (*qoder.SessionContext, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.accountStates[accountID]
	if !ok || state.generation != generation || state.credentialsHash != hash {
		return nil, false
	}
	entry, ok := p.sessions[accountID]
	if !ok || entry.credentialsHash != hash || entry.session == nil || qoderSessionCacheEntryExpired(entry, time.Now()) {
		delete(p.sessions, accountID)
		return nil, true
	}
	return entry.session, true
}

// storeQoderSessionBuild 只允许当前凭据世代写入，防止慢速旧交换覆盖新 session。
func (p *QoderTokenProvider) storeQoderSessionBuild(accountID int64, hash string, generation uint64, session *qoder.SessionContext, expiresAt time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.accountStates[accountID]
	if !ok || state.generation != generation || state.credentialsHash != hash {
		return false
	}
	p.sessions[accountID] = qoderSessionCacheEntry{credentialsHash: hash, session: session, expiresAt: expiresAt}
	return true
}

// qoderSessionCacheEntryExpired 判断带明确有效期的国内 PAT session 是否已经过期。
func qoderSessionCacheEntryExpired(entry qoderSessionCacheEntry, now time.Time) bool {
	return !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt)
}

func (p *QoderTokenProvider) Invalidate(accountID int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.accountStates == nil {
		p.accountStates = make(map[int64]qoderSessionAccountState)
	}
	state := p.accountStates[accountID]
	state.generation++
	p.accountStates[accountID] = state
	delete(p.sessions, accountID)
	p.mu.Unlock()
}

// InvalidateAccount 使用已从数据库或刷新结果取得的权威账号快照失效旧 session，封住新 token 尚未被请求观察到的窗口。
func (p *QoderTokenProvider) InvalidateAccount(account *Account) {
	if p == nil || account == nil {
		return
	}
	snapshot := snapshotQoderSessionAccount(account)
	hash := qoderCredentialsHash(snapshot.Credentials)

	p.mu.Lock()
	if p.sessions == nil {
		p.sessions = make(map[int64]qoderSessionCacheEntry)
	}
	if p.accountStates == nil {
		p.accountStates = make(map[int64]qoderSessionAccountState)
	}
	state := p.accountStates[snapshot.ID]
	incomingVersion := snapshot.GetCredentialAsInt64("_token_version")
	if current := state.accountSnapshot; current != nil {
		if qoderCredentialSnapshotOlder(snapshot, current) {
			// 迟到的刷新/DB 快照不能删除已经缓存的更新 session，也不能提升失效世代。
			p.mu.Unlock()
			return
		}
	}
	state.credentialsHash = hash
	state.credentialVersion = incomingVersion
	state.accountSnapshot = snapshot
	state.generation++
	p.accountStates[snapshot.ID] = state
	delete(p.sessions, snapshot.ID)
	p.mu.Unlock()
}

func (p *QoderTokenProvider) buildSession(ctx context.Context, account *Account) (*qoder.SessionContext, time.Time, error) {
	site, err := qoderSiteForAccount(account)
	if err != nil {
		return nil, time.Time{}, err
	}
	pat := strings.TrimSpace(account.GetCredential("pat"))
	if pat == "" {
		refreshMode, modeErr := qoderRefreshModeForAccount(account)
		if modeErr != nil {
			return nil, time.Time{}, modeErr
		}
		if refreshMode == qoder.RefreshModeQoderCN20 && site != qoder.SiteCN {
			return nil, time.Time{}, errors.New("qoder qodercn20 credentials require cn site")
		}
	}
	if pat != "" {
		machine := qoderMachineForAccount(account)
		var identity *qoder.AuthIdentity
		var expiresAt time.Time
		if site == qoder.SiteCN {
			exchangePAT := p.exchangeCNPAT
			if exchangePAT == nil {
				exchangePAT = p.defaultExchangeCNPAT(account)
			}
			identity, expiresAt, err = exchangePAT(ctx, pat, machine)
		} else {
			exchangePAT := p.exchangePAT
			if exchangePAT == nil {
				exchangePAT = p.defaultExchangePAT(account)
			}
			identity, err = exchangePAT(ctx, pat, machine)
		}
		if err != nil {
			// PAT exchange 失败通常是永久错误（无效凭据），不跳过缓存
			return nil, time.Time{}, fmt.Errorf("qoder pat exchange: %w", err)
		}
		applyQoderAccountIdentityMetadata(identity, account)
		// populateOrganizationFromAPI 在 session 创建前调用，避免缓存后并发修改 identity
		if site == qoder.SiteGlobal {
			p.populateOrganizationFromAPI(ctx, account, identity)
		}
		session, sessionErr := qoder.NewSessionForSite(identity, machine, site)
		return session, expiresAt, sessionErr
	}

	token := strings.TrimSpace(account.GetCredential("security_oauth_token"))
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if token != "" {
		if machineID == "" {
			return nil, time.Time{}, errors.New("qoder credentials require machine_id with security_oauth_token")
		}
		if firstNonEmptyQoder(account.GetCredential("uid"), account.GetCredential("aid")) == "" {
			return nil, time.Time{}, errors.New("qoder credentials require uid or aid with security_oauth_token")
		}
		identity := &qoder.AuthIdentity{
			Name:               firstNonEmptyQoder(account.GetCredential("name"), account.Name),
			AID:                firstNonEmptyQoder(account.GetCredential("aid"), account.GetCredential("uid")),
			UID:                firstNonEmptyQoder(account.GetCredential("uid"), account.GetCredential("aid")),
			UserType:           firstNonEmptyQoder(account.GetCredential("user_type"), "personal_standard"),
			SecurityOauthToken: token,
			RefreshToken:       account.GetCredential("refresh_token"),
		}
		applyQoderAccountIdentityMetadata(identity, account)
		p.populateOrganizationFromAPI(ctx, account, identity)
		machine := qoderMachineForAccount(account)
		session, sessionErr := qoder.NewSessionForSite(identity, machine, site)
		return session, time.Time{}, sessionErr
	}

	return nil, time.Time{}, errors.New("qoder credentials require pat or security_oauth_token+machine_id")
}

func (p *QoderTokenProvider) defaultExchangePAT(account *Account) qoderPATExchanger {
	return func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return qoder.ExchangePATContext(ctx, pat, machine, "", newQoderRequestDoer(account, p.httpUpstream, p.tlsFPProfileService))
	}
}

func (p *QoderTokenProvider) defaultExchangeCNPAT(account *Account) qoderCNPATExchanger {
	return func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		profile, err := qoder.ProfileForSite(qoder.SiteCN)
		if err != nil {
			return nil, time.Time{}, err
		}
		return qoder.ExchangeQoderCN20PATContext(ctx, pat, machine, profile, newQoderRequestDoer(account, p.httpUpstream, p.tlsFPProfileService))
	}
}

func (p *QoderTokenProvider) populateOrganizationFromAPI(ctx context.Context, account *Account, identity *qoder.AuthIdentity) {
	if p == nil || identity == nil {
		return
	}
	if strings.TrimSpace(identity.OrganizationID) != "" {
		return
	}
	token := strings.TrimSpace(identity.SecurityOauthToken)
	if token == "" {
		return
	}
	uid := firstNonEmptyQoder(identity.UID, identity.AID)
	if uid == "" {
		return
	}
	var tags *qoder.OrganizationTags
	var err error
	if p.getOrgTags != nil {
		tags, err = p.getOrgTags(ctx, token, uid)
	} else {
		tags, err = p.getOrganizationTagsForAccount(ctx, account, token, uid)
	}
	if err != nil || tags == nil {
		return
	}
	identity.OrganizationID = strings.TrimSpace(tags.OrganizationID)
	identity.OrganizationName = strings.TrimSpace(tags.OrganizationName)
}

func (p *QoderTokenProvider) getOrganizationTagsForAccount(ctx context.Context, account *Account, token, uid string) (*qoder.OrganizationTags, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, fmt.Errorf("qoder: organization tags require uid")
	}
	profile, err := qoderProfileForAccount(account)
	if err != nil {
		return nil, err
	}
	if doer := newQoderRequestDoer(account, p.httpUpstream, p.tlsFPProfileService); doer != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, profile.OpenAPIBaseURL+qoder.OrganizationTagsPathPrefix+url.PathEscape(uid)+"/tags", nil)
		if err != nil {
			return nil, fmt.Errorf("qoder: create organization tags request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		req.Header.Set("User-Agent", profile.OpenAPIUserAgent())

		resp, err := doer(req)
		if err != nil {
			return nil, fmt.Errorf("qoder: organization tags request: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return nil, fmt.Errorf("qoder: organization tags failed with status %d: %s", resp.StatusCode, qoder.RedactSensitiveText(string(body)))
		}

		var tags qoder.OrganizationTags
		if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
			return nil, fmt.Errorf("qoder: parse organization tags response: %w", err)
		}
		return &tags, nil
	}
	return qoder.NewOAuthClientForProfile(profile, nil).GetOrganizationTags(ctx, token, uid)
}

func applyQoderAccountIdentityMetadata(identity *qoder.AuthIdentity, account *Account) {
	if identity == nil || account == nil {
		return
	}
	if strings.TrimSpace(identity.Name) == "" {
		identity.Name = firstNonEmptyQoder(account.GetCredential("name"), account.Name)
	}
	if strings.TrimSpace(identity.OrganizationID) == "" {
		identity.OrganizationID = account.GetCredential("organization_id")
	}
	if strings.TrimSpace(identity.OrganizationName) == "" {
		identity.OrganizationName = account.GetCredential("organization_name")
	}
}

func qoderCredentialsHash(credentials map[string]any) string {
	body, _ := json.Marshal(credentials)
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum[:])
}

func qoderRefreshCredentialsHash(credentials map[string]any) string {
	if len(credentials) == 0 {
		return qoderCredentialsHash(nil)
	}
	keys := []string{
		"pat",
		"security_oauth_token",
		"refresh_token",
		"machine_id",
		"machine_token",
		"machine_type",
		"uid",
		"aid",
		"organization_id",
		"organization_name",
		"name",
		"user_type",
		"site",
		"refresh_mode",
	}
	auth := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := credentials[key]; ok {
			auth[key] = value
		}
	}
	return qoderCredentialsHash(auth)
}

func firstNonEmptyQoder(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
