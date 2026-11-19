package identity

import (
	"context"
	"log/slog"
	"strings"
)

// dingtalk_profile_sync.go synchronizes selected DingTalk staff fields into
// local profiles and attributes and ensures sync attribute definitions exist.
// DingTalk profile synchronization writes selected staff values after an
// eligible internal-organization login, while administrator settings
// application reconciles enabled sync attribute definitions under internal_only
// policy. The supplied switches choose email, display name and department
// attributes, while the caller separately permits username changes for account
// creation. An unavailable attribute service stops the entire update, and later
// lookup or write errors allow other selected attributes to continue after
// diagnostics.
//
// Direct staff snapshots can carry several department IDs, but pending upstream
// claims retain one primary ID. Synchronization chooses the first non-root ID
// available in its supplied snapshot, then falls back to the first membership.
// Claim reconstruction accepts integer or JSON-decoded floating representations
// of that saved ID; it cannot recreate department memberships absent from the
// claims. Every submitted attribute value is checked under the configured
// definition's normal validation, including an empty department path.

// DingTalkProfileSnapshot holds provider staff values selected for local
// account synchronization.
type DingTalkProfileSnapshot struct {
	// UserID identifies staff within the organization; Name and Nickname supply
	// separate display candidates, and Email carries the provider's staff email.
	// UserID: organization-local staff identifier. Name: supplied staff name.
	// Nickname: supplied staff nickname. Email: supplied staff email.
	UserID, Name, Nickname, Email string
	// DeptIDs carries supplied organization memberships. Async synchronization
	// retains this slice through its snapshot, so callers keep it stable until
	// work ends.
	DeptIDs []int64
}

// DingTalkSyncOptions selects staff fields and attribute keys for the current
// login.
type DingTalkSyncOptions struct {
	// CorpRestrictionPolicy must equal internal_only for Sync to write local
	// profile data; other policy values skip both username and attribute changes.
	CorpRestrictionPolicy string
	// SyncCorpEmail, SyncDisplayName and SyncDept choose which staff fields are
	// submitted under their corresponding configured attribute keys. Setting all
	// three false stops synchronization, including a requested username update.
	// SyncCorpEmail: corporate email synchronization flag. SyncDisplayName:
	// display name synchronization flag. SyncDept: department synchronization
	// flag.
	SyncCorpEmail, SyncDisplayName, SyncDept bool
	// SyncCorpEmailAttrKey, SyncDisplayNameAttrKey and SyncDeptAttrKey are paired
	// with their matching field switches. Each enabled value resolves its
	// definition by key before the normal validated attribute write.
	// SyncCorpEmailAttrKey: corporate email attribute key. SyncDisplayNameAttrKey:
	// display name attribute key. SyncDeptAttrKey: department path attribute key.
	SyncCorpEmailAttrKey, SyncDisplayNameAttrKey, SyncDeptAttrKey string
}

// DingTalkDepartmentReader resolves organization membership into the stored
// display path.
type DingTalkDepartmentReader interface {
	// ResolveDepartmentPath returns the display path for a supplied department ID.
	// A successful empty path can represent root-only membership; synchronization
	// submits that value for ordinary attribute validation, while lookup errors
	// skip it.
	ResolveDepartmentPath(context.Context, int64) (string, error)
}

// DingTalkDepartmentFunc adapts one directory lookup function to the department
// reader.
type DingTalkDepartmentFunc func(context.Context, int64) (string, error)

type (
	// ProfileSyncObserver sends profile-sync diagnostics to the logger selected by
	// app.
	ProfileSyncObserver func(level, message string, args ...any)
	// DingTalkProfileSync writes selected provider staff values through local user
	// services.
	DingTalkProfileSync struct {
		// Users applies username changes when Sync's caller enables them and the
		// display-name switch is set. A nil service skips that username update.
		Users *UserService
		// Attributes resolves definitions and writes validated values. When this
		// service is nil, Sync returns before either username or attribute changes.
		Attributes *UserAttributeService
		// Observe receives synchronization diagnostics when configured. A nil
		// callback discards them, and callers keep it stable while synchronization
		// runs.
		Observe ProfileSyncObserver
	}
)

// ResolveDepartmentPath forwards the directory lookup to the supplied function.
// Callers configure a non-nil function and pass the operation context; its
// display path and error are returned for synchronization to interpret.
func (f DingTalkDepartmentFunc) ResolveDepartmentPath(ctx context.Context, id int64) (string, error) {
	return f(ctx, id)
}

// log forwards synchronization diagnostics to the optional observer callback.
// An unset observer discards the message, letting profile updates and skip
// paths complete when no synchronization-specific logging function has been
// configured.
func (s *DingTalkProfileSync) log(level, message string, args ...any) {
	if s.Observe != nil {
		s.Observe(level, message, args...)
	}
}

// Sync writes enabled staff fields to local account attributes and optionally
// updates the username. It requires internal_only policy, a staff snapshot and
// an attribute service before any update; callers enable syncUsername when that
// account flow permits the supplied display name to change users.username.
// Department synchronization with supplied memberships also requires a usable
// directory client because the lookup is invoked directly for that selection.
//
// Nonblank names and emails become configured attribute writes. Department
// selection prefers the first supplied ID above one and otherwise uses the
// first membership; a successful empty path is still submitted to attribute
// validation. Username, directory and individual attribute failures are
// diagnosed separately, so completed writes remain and later selected fields
// can continue.
func (s *DingTalkProfileSync) Sync(ctx context.Context, cfg DingTalkSyncOptions, client DingTalkDepartmentReader, userID int64, staff *DingTalkProfileSnapshot, syncUsername bool) {
	s.log("info", "dingtalk sync: entry",
		"user_id", userID,
		"policy", cfg.CorpRestrictionPolicy,
		"sync_corp_email", cfg.SyncCorpEmail,
		"sync_display_name", cfg.SyncDisplayName,
		"sync_dept", cfg.SyncDept,
		"sync_username", syncUsername,
		"attr_key_email", cfg.SyncCorpEmailAttrKey,
		"attr_key_name", cfg.SyncDisplayNameAttrKey,
		"attr_key_dept", cfg.SyncDeptAttrKey,
		"staff_nil", staff == nil,
	)
	if cfg.CorpRestrictionPolicy != "internal_only" || staff == nil {
		s.log("info", "dingtalk sync: skip, not internal_only or staff nil")
		return
	}
	s.log("info", "dingtalk sync: staff snapshot",
		"name", staff.Name, "email", staff.Email, "dept_ids", staff.DeptIDs,
	)
	if !cfg.SyncCorpEmail && !cfg.SyncDisplayName && !cfg.SyncDept {
		s.log("info", "dingtalk sync: skip, all flags disabled")
		return
	}
	if s.Attributes == nil {
		s.log("warn", "dingtalk sync: userAttributeService not available, skipping")
		return
	}

	// Callers enable username synchronization at signup to retain later user
	// edits. The display-name attribute is synchronized separately on every
	// eligible login.
	if syncUsername && cfg.SyncDisplayName {
		username := strings.TrimSpace(staff.Nickname)
		source := "nickname"
		// A missing nickname leaves the staff name as the available username
		// candidate for this caller-permitted update, matching the supplied display
		// identity.
		if username == "" {
			username = strings.TrimSpace(staff.Name)
			source = "name(fallback)"
		}
		// Username failure leaves the selected attributes available for later writes;
		// diagnose that failure while this best-effort login profile update
		// continues.
		if username != "" && s.Users != nil {
			if _, err := s.Users.UpdateProfile(ctx, userID, UpdateProfileRequest{Username: &username}); err != nil {
				s.log("warn", "dingtalk sync: failed to update username", "user_id", userID, "err", err)
			} else {
				s.log("info", "dingtalk sync: username updated (register)", "user_id", userID, "username", username, "source", source)
			}
		}
	}

	// syncField pairs one configured attribute key with selected staff text.
	type syncField struct {
		key   string // configured attribute definition key
		value string // selected staff attribute text
	}
	var fields []syncField

	if cfg.SyncDisplayName && strings.TrimSpace(staff.Name) != "" {
		fields = append(fields, syncField{cfg.SyncDisplayNameAttrKey, strings.TrimSpace(staff.Name)})
	}
	if cfg.SyncCorpEmail && strings.TrimSpace(staff.Email) != "" {
		fields = append(fields, syncField{cfg.SyncCorpEmailAttrKey, strings.TrimSpace(staff.Email)})
	}
	if cfg.SyncDept && len(staff.DeptIDs) > 0 {
		// Department ID one represents the root in this flow. Prefer a supplied
		// membership above one to obtain a useful subdepartment path, then use the
		// first membership when no preferred ID is available.
		primaryDeptID := int64(0)
		for _, id := range staff.DeptIDs {
			if id > 1 {
				primaryDeptID = id
				break
			}
		}
		if primaryDeptID == 0 {
			primaryDeptID = staff.DeptIDs[0]
		}
		s.log("info", "dingtalk sync: pick primary dept", "user_id", userID, "all_dept_ids", staff.DeptIDs, "primary", primaryDeptID)
		path, err := client.ResolveDepartmentPath(ctx, primaryDeptID)
		if err != nil {
			// A directory failure leaves the saved department value untouched here,
			// while name and email selections remain available for their attribute
			// writes.
			s.log("warn", "dingtalk sync: failed to resolve dept path", "user_id", userID, "dept_id", primaryDeptID, "err", err)
		} else {
			// Root-only lookup can succeed with an empty path. Submit it through
			// attribute validation so an optional saved path can clear, while a required
			// definition can reject that empty value.
			fields = append(fields, syncField{cfg.SyncDeptAttrKey, path})
		}
	}

	if len(fields) == 0 {
		return
	}

	// Write each selected attribute separately; a lookup or validation failure for
	// one definition leaves other eligible values available for subsequent
	// attempts.
	for _, f := range fields {
		if err := s.setUserAttributeByKey(ctx, userID, f.key, f.value); err != nil {
			s.log("warn", "dingtalk sync: failed to set attribute", "user_id", userID, "key", f.key, "err", err)
		}
	}
}

// setUserAttributeByKey resolves a definition and submits one validated value.
// Lookup failures are diagnosed and return nil so synchronization can skip an
// unavailable definition; validation or value-write failures remain errors for
// Sync to diagnose while continuing other selected fields.
func (s *DingTalkProfileSync) setUserAttributeByKey(ctx context.Context, userID int64, key, value string) error {
	def, err := s.Attributes.GetDefinitionByKey(ctx, key)
	if err != nil {
		// Missing or unreadable definitions cannot identify a safe attribute write.
		// Treat this selected field as skipped while the caller continues its other
		// fields.
		s.log("warn", "dingtalk sync: attribute definition not found, skipping", "key", key, "err", err.Error())
		return nil
	}
	if err := s.Attributes.UpdateUserAttributes(ctx, userID, []UpdateUserAttributeInput{
		{AttributeID: def.ID, Value: value},
	}); err != nil {
		return err
	}
	s.log("info", "dingtalk sync: attribute upserted", "user_id", userID, "key", key, "attr_id", def.ID)
	return nil
}

// DingTalkProfileFromClaims rebuilds a staff snapshot from saved upstream
// claims. Nil claims return an allocated empty snapshot; string fields are used
// only when their decoded values have the expected type.
//
// The claims carry one primary_dept_id rather than the complete membership
// list. Positive int64 values are retained, and float64 values are converted to
// int64 before the positivity check, accommodating the JSON-decoded
// representation. Float64 claims supply their converted integer value, so
// synchronization receives the primary ID represented by that value after
// conversion.
func DingTalkProfileFromClaims(claims map[string]any) *DingTalkProfileSnapshot {
	if claims == nil {
		return &DingTalkProfileSnapshot{}
	}
	staff := &DingTalkProfileSnapshot{}
	if v, ok := claims["username"].(string); ok {
		staff.Name = v
	}
	if v, ok := claims["nickname"].(string); ok {
		staff.Nickname = v
	}
	if v, ok := claims["email"].(string); ok {
		staff.Email = v
	}
	if v, ok := claims["corp_user_id"].(string); ok {
		staff.UserID = v
	}
	// Saved claims can decode numeric IDs as float64; accept that representation
	// after integer conversion so the pending flow can still reconstruct its one
	// department.
	switch v := claims["primary_dept_id"].(type) {
	case int64:
		if v > 0 {
			staff.DeptIDs = []int64{v}
		}
	case float64:
		if id := int64(v); id > 0 {
			staff.DeptIDs = []int64{id}
		}
	}
	return staff
}

// EnsureDingTalkSyncAttributes creates or updates enabled DingTalk profile-sync
// user attribute definitions when settings use the internal_only corporate
// policy. A nil attribute service or non-internal_only policy returns without
// querying storage, and individual lookup, update, or creation failures log a
// warning while allowing remaining enabled attributes to proceed.
func EnsureDingTalkSyncAttributes(ctx context.Context, attributes *UserAttributeService, settings AdminSettings) {
	if attributes == nil || settings.DingTalkConnectCorpRestrictionPolicy != "internal_only" {
		return
	}
	if settings.DingTalkConnectSyncDisplayName {
		ensureDingTalkAttributeDefinition(ctx, attributes, settings.DingTalkConnectSyncDisplayNameAttrKey, settings.DingTalkConnectSyncDisplayNameAttrName, "钉钉 internal_only 登录时同步的钉钉姓名", AttributeTypeText)
	}
	if settings.DingTalkConnectSyncCorpEmail {
		ensureDingTalkAttributeDefinition(ctx, attributes, settings.DingTalkConnectSyncCorpEmailAttrKey, settings.DingTalkConnectSyncCorpEmailAttrName, "钉钉 internal_only 登录时同步的企业邮箱", AttributeTypeEmail)
	}
	if settings.DingTalkConnectSyncDept {
		ensureDingTalkAttributeDefinition(ctx, attributes, settings.DingTalkConnectSyncDeptAttrKey, settings.DingTalkConnectSyncDeptAttrName, "钉钉 internal_only 登录时同步的完整部门路径（如：公司/研发部）", AttributeTypeText)
	}
}

// ensureDingTalkAttributeDefinition reconciles one trimmed attribute definition
// key after settings persistence. Found definitions receive nonempty display
// name updates while keeping stored type and validation rules; missing
// definitions or lookup errors fall through to creation and log warnings on
// failure without aborting settings application.
func ensureDingTalkAttributeDefinition(ctx context.Context, attributes *UserAttributeService, key, name, description string, attrType UserAttributeType) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	// Fall through to CreateDefinition when lookup fails or returns nil, and log
	// write failures as warnings so post-commit settings application never fails.
	existing, err := attributes.GetDefinitionByKey(ctx, key)
	if err == nil && existing != nil {
		if strings.TrimSpace(name) != "" && existing.Name != name {
			if _, err := attributes.UpdateDefinition(ctx, existing.ID, UpdateAttributeDefinitionInput{
				Name: &name,
			}); err != nil {
				slog.Warn("dingtalk: update user attribute definition name failed", "key", key, "err", err.Error())
				return
			}
			slog.Info("dingtalk: updated user attribute definition name", "key", key, "name", name)
		}
		return
	}
	if _, err := attributes.CreateDefinition(ctx, CreateAttributeDefinitionInput{
		Key:         key,
		Name:        name,
		Description: description,
		Type:        attrType,
		Enabled:     true,
	}); err != nil {
		slog.Warn("dingtalk: ensure user attribute definition failed", "key", key, "err", err.Error())
		return
	}
	slog.Info("dingtalk: created user attribute definition", "key", key, "name", name, "type", attrType)
}
