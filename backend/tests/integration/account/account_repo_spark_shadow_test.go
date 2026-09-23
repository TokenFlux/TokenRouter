//go:build integration

package account_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func TestAccountRepoSparkShadowRoundTrip(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountStoreContract(tx.Client(), tx, nil)

	parent := &account.Record{
		Name:     "parent",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
	}
	if err := repo.Create(ctx, parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	pid := parent.ID
	shadow := &account.Record{
		Name:            "shadow",
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Status:          billing.StatusActive,
		ParentAccountID: &pid,
		QuotaDimension:  account.QuotaDimensionSpark,
	}
	if err := repo.Create(ctx, shadow); err != nil {
		t.Fatalf("create shadow: %v", err)
	}
	got, err := repo.GetByID(ctx, shadow.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ParentAccountID == nil || *got.ParentAccountID != pid {
		t.Fatalf("ParentAccountID round-trip: %v", got.ParentAccountID)
	}
	if got.QuotaDimension != account.QuotaDimensionSpark {
		t.Fatalf("QuotaDimension: %q", got.QuotaDimension)
	}
}

func TestListShadowsByParent(t *testing.T) {
	// Schema enforces at most one spark shadow per parent (uq_accounts_spark_shadow_per_parent).
	// Test strategy: create 2 parents each with 1 spark shadow + 1 unrelated account;
	// assert ListShadowsByParent(parent1.ID) returns exactly 1 (filtering by both
	// parent_account_id and quota_dimension='spark', excluding parent2's shadow and unrelated).
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountStoreContract(tx.Client(), tx, nil)

	// Create parent1 and its spark shadow
	parent1 := &account.Record{
		Name:     "list-parent1",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
	}
	if err := repo.Create(ctx, parent1); err != nil {
		t.Fatalf("create parent1: %v", err)
	}
	pid1 := parent1.ID

	shadow1 := &account.Record{
		Name:            "shadow1",
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Status:          billing.StatusActive,
		ParentAccountID: &pid1,
		QuotaDimension:  account.QuotaDimensionSpark,
	}
	if err := repo.Create(ctx, shadow1); err != nil {
		t.Fatalf("create shadow1: %v", err)
	}

	// Create parent2 and its spark shadow (must NOT appear in parent1's list)
	parent2 := &account.Record{
		Name:     "list-parent2",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
	}
	if err := repo.Create(ctx, parent2); err != nil {
		t.Fatalf("create parent2: %v", err)
	}
	pid2 := parent2.ID

	shadow2 := &account.Record{
		Name:            "shadow2",
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Status:          billing.StatusActive,
		ParentAccountID: &pid2,
		QuotaDimension:  account.QuotaDimensionSpark,
	}
	if err := repo.Create(ctx, shadow2); err != nil {
		t.Fatalf("create shadow2: %v", err)
	}

	// Create 1 unrelated normal account (no parent, global dimension)
	unrelated := &account.Record{
		Name:     "unrelated",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
	}
	if err := repo.Create(ctx, unrelated); err != nil {
		t.Fatalf("create unrelated: %v", err)
	}

	// Assert ListShadowsByParent returns exactly 1 for parent1
	got, err := repo.ListShadowsByParent(ctx, pid1)
	if err != nil {
		t.Fatalf("ListShadowsByParent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 spark shadow for parent1, got %d", len(got))
	}
	acc := got[0]
	if acc.ParentAccountID == nil || *acc.ParentAccountID != pid1 {
		t.Errorf("unexpected ParentAccountID: %v", acc.ParentAccountID)
	}
	if acc.QuotaDimension != account.QuotaDimensionSpark {
		t.Errorf("unexpected QuotaDimension: %q", acc.QuotaDimension)
	}
	if acc.ID != shadow1.ID {
		t.Errorf("expected shadow1.ID=%d, got %d", shadow1.ID, acc.ID)
	}
}
