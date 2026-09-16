// 身份归属规则区分风险行为用户与付款用户，不扩大审核或管理员豁免范围。
package moderationflow

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

type Identity struct {
	UserID        int64
	UserEmail     string
	BillingUserID int64
	TeamID        *int64
}

func ResolveIdentity(apiKey *apikey.APIKey, subjectUserID int64) Identity {
	identity := Identity{
		UserID:        subjectUserID,
		BillingUserID: subjectUserID,
	}
	if apiKey == nil {
		return identity
	}

	if apiKey.UserID > 0 {
		identity.UserID = apiKey.UserID
		if identity.BillingUserID <= 0 {
			identity.BillingUserID = apiKey.UserID
		}
	}
	if apiKey.User != nil && apiKey.User.ID > 0 {
		identity.BillingUserID = apiKey.User.ID
		if apiKey.User.ID == identity.UserID {
			identity.UserEmail = strings.TrimSpace(apiKey.User.Email)
		}
	}
	if apiKey.ActorUser != nil && apiKey.ActorUser.ID > 0 {
		identity.UserID = apiKey.ActorUser.ID
		identity.UserEmail = strings.TrimSpace(apiKey.ActorUser.Email)
	} else if apiKey.TeamMembership != nil && apiKey.TeamMembership.UserID == identity.UserID {
		identity.UserEmail = strings.TrimSpace(apiKey.TeamMembership.Email)
	}
	identity.TeamID = cloneID(apiKey.TeamID)
	return identity
}
