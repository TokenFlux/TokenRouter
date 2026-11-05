package schema

import (
	"fmt"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// user.go defines local account profiles, status, wallet amounts, and request
// limits. Authentication identities and team memberships are separate
// relationships, while billing updates the user's available and reserved
// wallet amounts and notification settings describe the user's alert
// preferences.

// User stores local account, wallet, profile, and admission settings.
type User struct {
	ent.Schema // generated entity definition
}

// Annotations maps local account persistence to the shared users database table.
func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "users"},
	}
}

// Mixin supplies timestamps and default soft-deletion behavior for user records.
// Ordinary Ent queries exclude deleted users, while maintenance can access
// retained rows through a SkipSoftDelete context or remove them physically.
func (User) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

// Fields describes account identity, wallet, limits, profile, and notification
// preferences. Generated Ent inserts start active with the user role, five
// concurrent requests, and zero wallet amounts. Signup-source validation accepts
// the seven supported authentication source names exactly.
func (User) Fields() []ent.Field {
	return []ent.Field{
		// Migration 016's partial unique index includes users with deleted_at
		// NULL. Soft-deleted users leave that index, allowing their email
		// addresses to be reused while undeleted users remain unique.
		field.String("email").
			MaxLen(255).
			NotEmpty(),
		// Stored password digest.
		field.String("password_hash").
			MaxLen(255).
			NotEmpty(),
		// Global account role.
		field.String("role").
			MaxLen(20).
			Default(identity.RoleUser),
		// balance and frozen_balance distinguish spendable wallet funds from
		// task reservations. Task reservation subtracts from balance and adds
		// to frozen_balance; release returns the reserved amount, and capture
		// clears the hold while reconciling the actual charge.
		// USD spendable balance.
		field.Float("balance").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0),
		// USD reserved balance.
		field.Float("frozen_balance").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0),
		// A missing override follows the current global recharge multiplier.
		// Stored positive values apply only to newly created balance orders.
		field.Float("balance_recharge_multiplier").
			SchemaType(map[string]string{dialect.Postgres: "double precision"}).
			Optional().
			Nillable().
			Validate(func(value float64) error {
				if !identity.IsValidBalanceRechargeMultiplier(value) {
					return identity.ErrBalanceRechargeMultiplierInvalid
				}
				return nil
			}),
		// Concurrent request allowance.
		field.Int("concurrency").
			Default(5),
		// Account activation state.
		field.String("status").
			MaxLen(20).
			Default(identity.StatusActive),

		// preferred_locale records the user's chosen interface and notification
		// language. When absent, presentation and mail consumers resolve a
		// language from their other configured inputs.
		field.String("preferred_locale").MaxLen(35).Optional().Nillable(),
		// User display name.
		field.String("username").
			MaxLen(100).
			Default(""),
		// The wechat profile field lives in user_attribute_values; see migration 019.
		// Free-form account notes.
		field.String("notes").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),

		// totp_secret_encrypted, totp_enabled, and totp_enabled_at hold the
		// persistent TOTP configuration. CompleteSetup verifies the cached setup
		// code before saving the encrypted seed, then separately writes activation
		// and its time. DisableTotp clears the seed and time with the activation flag.
		//
		// totp_secret_encrypted stores the confirmed encrypted seed before
		// CompleteSetup requests activation. If enabling TOTP fails, the
		// seed-writing call has already succeeded.
		field.String("totp_secret_encrypted").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Optional().
			Nillable(),
		// TOTP activation flag.
		field.Bool("totp_enabled").
			Default(false),
		// TOTP activation timestamp.
		field.Time("totp_enabled_at").
			Optional().
			Nillable(),
		// Initial authentication source.
		field.String("signup_source").
			Validate(func(value string) error {
				switch value {
				case "email", "linuxdo", "wechat", "oidc", "github", "google", "dingtalk":
					return nil
				default:
					return fmt.Errorf("must be one of email, linuxdo, wechat, oidc, github, google, dingtalk")
				}
			}).
			Default("email"),
		// Latest login timestamp.
		field.Time("last_login_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// Latest activity timestamp.
		field.Time("last_active_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),

		// balance_notify_enabled, balance_notify_threshold_type, and
		// balance_notify_threshold configure the user's low-balance alert.
		// EffectiveBalanceThreshold uses the global threshold when the user
		// amount is absent and converts percentage values using positive total_recharged.
		// User alert preference.
		field.Bool("balance_notify_enabled").
			Default(true),
		// Fixed/percentage threshold mode.
		field.String("balance_notify_threshold_type").
			Default("fixed"), // "fixed" | "percentage"
		// Optional alert threshold.
		field.Float("balance_notify_threshold").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Optional().
			Nillable(),
		// balance_notify_extra_emails contains JSON recipient records. The
		// notification consumer selects verified, enabled addresses and
		// deduplicates them case-insensitively when collecting alert recipients.
		field.String("balance_notify_extra_emails").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default("[]"),
		// total_recharged supplies cumulative recharge amount for percentage
		// balance alerts. BalanceThreshold multiplies a positive recharge total
		// by the configured percentage divided by one hundred.
		field.Float("total_recharged").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0),

		// rpm_limit is the user-wide admission limit checked after group limits.
		// Zero disables this user's limit while other request and group checks
		// continue to apply at their own admission stages.
		field.Int("rpm_limit").
			Default(0),
		// api_key_limit bounds creation in KeyStore.Create using the count of
		// this user's undeleted keys. Zero disables the cap; Ent validates the
		// stored value against identity's configured maximum.
		field.Int("api_key_limit").
			Default(identity.DefaultUserAPIKeyLimit).
			Range(0, identity.MaxUserAPIKeyLimit),
	}
}

// Edges relates accounts to keys, grants, subscriptions, usage, profile values,
// authentication, and teams. Physical user deletion cascades to canonical
// identities; other relationships retain their separately declared delete actions.
func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("api_keys", APIKey.Type),
		edge.To("redeem_codes", RedeemCode.Type),
		edge.To("redeem_code_usages", RedeemCodeUsage.Type),
		edge.To("subscriptions", UserSubscription.Type),
		edge.To("assigned_subscriptions", UserSubscription.Type),
		edge.To("announcement_reads", AnnouncementRead.Type),
		edge.To("allowed_groups", Group.Type).
			Through("user_allowed_groups", UserAllowedGroup.Type),
		edge.To("disabled_public_groups", Group.Type).
			Through("user_disabled_public_groups", UserDisabledPublicGroup.Type),
		edge.To("usage_logs", UsageLog.Type),
		edge.To("attribute_values", UserAttributeValue.Type),
		edge.To("promo_code_usages", PromoCodeUsage.Type),
		edge.To("payment_orders", PaymentOrder.Type),
		edge.To("auth_identities", AuthIdentity.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("pending_auth_sessions", PendingAuthSession.Type),
		edge.To("team_memberships", TeamMembership.Type),
	}
}

// Indexes supports activation filtering and retained-row maintenance for accounts.
// Migration 016's partial index enforces email uniqueness among undeleted
// users, while the indexes declared here support status and deletion lookup.
func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("deleted_at"),
	}
}
