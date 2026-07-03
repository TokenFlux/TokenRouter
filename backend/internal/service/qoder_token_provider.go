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

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
)

type qoderPATExchanger func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error)
type qoderOrganizationTagsGetter func(ctx context.Context, token, uid string) (*qoder.OrganizationTags, error)

type qoderSessionCacheEntry struct {
	credentialsHash string
	session         *qoder.SessionContext
}

// QoderTokenProvider builds and caches COSY session contexts for Qoder accounts.
type QoderTokenProvider struct {
	mu                  sync.Mutex
	sessions            map[int64]qoderSessionCacheEntry
	exchangePAT         qoderPATExchanger
	getOrgTags          qoderOrganizationTagsGetter
	httpUpstream        HTTPUpstream
	tlsFPProfileService *TLSFingerprintProfileService
}

func NewQoderTokenProvider() *QoderTokenProvider {
	return &QoderTokenProvider{
		sessions: make(map[int64]qoderSessionCacheEntry),
	}
}

func (p *QoderTokenProvider) SetHTTPUpstream(httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) {
	if p == nil {
		return
	}
	p.httpUpstream = httpUpstream
	p.tlsFPProfileService = tlsFPProfileService
}

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

	hash := qoderCredentialsHash(account.Credentials)
	p.mu.Lock()
	if p.sessions == nil {
		p.sessions = make(map[int64]qoderSessionCacheEntry)
	}
	if entry, ok := p.sessions[account.ID]; ok && entry.credentialsHash == hash && entry.session != nil {
		// 在持有锁的情况下复制 session 指针，避免释放锁后被 Invalidate() 删除
		session := entry.session
		p.mu.Unlock()
		return session, nil
	}
	p.mu.Unlock()

	session, err := p.buildSession(ctx, account)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.sessions[account.ID] = qoderSessionCacheEntry{credentialsHash: hash, session: session}
	p.mu.Unlock()
	return session, nil
}

func (p *QoderTokenProvider) Invalidate(accountID int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	delete(p.sessions, accountID)
	p.mu.Unlock()
}

func (p *QoderTokenProvider) buildSession(ctx context.Context, account *Account) (*qoder.SessionContext, error) {
	pat := strings.TrimSpace(account.GetCredential("pat"))
	if pat != "" {
		machine := qoder.NewMachine()
		exchangePAT := p.exchangePAT
		if exchangePAT == nil {
			exchangePAT = p.defaultExchangePAT(account)
		}
		identity, err := exchangePAT(ctx, pat, machine)
		if err != nil {
			// PAT exchange 失败通常是永久错误（无效凭据），不跳过缓存
			return nil, fmt.Errorf("qoder pat exchange: %w", err)
		}
		applyQoderAccountIdentityMetadata(identity, account)
		// populateOrganizationFromAPI 在 session 创建前调用，避免缓存后并发修改 identity
		p.populateOrganizationFromAPI(ctx, account, identity)
		return qoder.NewSession(identity, machine)
	}

	token := strings.TrimSpace(account.GetCredential("security_oauth_token"))
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if token != "" {
		if machineID == "" {
			return nil, errors.New("qoder credentials require machine_id with security_oauth_token")
		}
		if firstNonEmptyQoder(account.GetCredential("uid"), account.GetCredential("aid")) == "" {
			return nil, errors.New("qoder credentials require uid or aid with security_oauth_token")
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
		machine := &qoder.MachineIdentity{
			MachineID:    machineID,
			MachineToken: firstNonEmptyQoder(account.GetCredential("machine_token"), qoder.RandomToken(50)),
			MachineType:  firstNonEmptyQoder(account.GetCredential("machine_type"), qoder.RandomHex(18)),
		}
		return qoder.NewSession(identity, machine)
	}

	return nil, errors.New("qoder credentials require pat or security_oauth_token+machine_id")
}

func (p *QoderTokenProvider) defaultExchangePAT(account *Account) qoderPATExchanger {
	return func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return qoder.ExchangePATContext(ctx, pat, machine, "", newQoderRequestDoer(account, p.httpUpstream, p.tlsFPProfileService))
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
	if doer := newQoderRequestDoer(account, p.httpUpstream, p.tlsFPProfileService); doer != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, qoder.OpenAPIBaseURL+qoder.OrganizationTagsPathPrefix+url.PathEscape(uid)+"/tags", nil)
		if err != nil {
			return nil, fmt.Errorf("qoder: create organization tags request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		req.Header.Set("User-Agent", "Go-http-client/2.0")

		resp, err := doer(req)
		if err != nil {
			return nil, fmt.Errorf("qoder: organization tags request: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return nil, fmt.Errorf("qoder: organization tags failed with status %d: %s", resp.StatusCode, string(body))
		}

		var tags qoder.OrganizationTags
		if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
			return nil, fmt.Errorf("qoder: parse organization tags response: %w", err)
		}
		return &tags, nil
	}
	return qoder.NewOAuthClient(qoder.OpenAPIBaseURL, nil).GetOrganizationTags(ctx, token, uid)
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

func firstNonEmptyQoder(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
