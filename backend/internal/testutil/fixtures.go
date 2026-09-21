//go:build unit

package testutil

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// NewTestUser 创建一个可用的测试用户，可通过 opts 覆盖默认值。
func NewTestUser(opts ...func(*identity.User)) *identity.User {
	u := &identity.User{
		ID:          1,
		Email:       "test@example.com",
		Username:    "testuser",
		Role:        "user",
		Balance:     100.0,
		Concurrency: 5,
		Status:      billing.StatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	for _, opt := range opts {
		opt(u)
	}
	return u
}

// NewTestAccount 创建一个可用的测试账户，可通过 opts 覆盖默认值。
func NewTestAccount(opts ...func(*service.Account)) *service.Account {
	a := &service.Account{
		ID:          1,
		Name:        "test-account",
		Platform:    capability.PlatformAnthropic,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 5,
		Priority:    1,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// NewTestAPIKey 创建一个可用的测试 API Key，可通过 opts 覆盖默认值。
func NewTestAPIKey(opts ...func(*apikey.APIKey)) *apikey.APIKey {
	groupID := int64(1)
	k := &apikey.APIKey{
		ID:        1,
		UserID:    1,
		Key:       "sk-test-key-12345678",
		Name:      "test-key",
		GroupID:   &groupID,
		Status:    billing.StatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	for _, opt := range opts {
		opt(k)
	}
	return k
}

// NewTestGroup 创建一个可用的测试分组，可通过 opts 覆盖默认值。
func NewTestGroup(opts ...func(*routing.Group)) *routing.Group {
	g := &routing.Group{
		ID:       1,
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
		Hydrated: true,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}
