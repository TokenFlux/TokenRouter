package usageprovider

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/internal/usageclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// newapi.go queries and normalizes New API token allowances, site display
// units, and multi-source wallet balances.
// New API reads display settings before token and wallet data so raw quota
// units can be converted consistently. Wallet discovery prefers embedded
// values, then a configured user-token query, then the fixed compatibility
// endpoint, with authentication and timeout outcomes limiting further attempts.
//
// A merged result requires matching units and retains key limits independently
// of its wallet. Token quota consistency uses CloseEnough, which rejects
// nonfinite sums before converting raw counters into display amounts.

// The optional New API display probe uses a short budget, while raw quota
// conversion uses its default divisor when no positive finite site factor is
// available.
const (
	StatusTimeout             = 2 * time.Second // optional display settings budget
	newAPIDefaultQuotaPerUnit = 500000.0        // raw quota units per USD
)

// NewAPIUsageAdapter combines validated token allowance with a separately
// queried user wallet.
type NewAPIUsageAdapter struct{}

// NewAPITokenUsageResponse holds token quota and optional embedded wallet
// observations from JSON.
type NewAPITokenUsageResponse struct {
	// Code and Success distinguish an explicit rejection from malformed token
	// data: either supplied false rejects authentication, while Code=true is
	// required and Success can be absent.
	Code    *bool  `json:"code"`    // required token success marker
	Success *bool  `json:"success"` // optional response success marker
	Message string `json:"message"` // reported upstream diagnostic text
	// Data is required after successful flags. Its token counters and expiry are
	// validated before normalization, while embedded wallet fields stay raw for
	// separate source selection.
	Data *struct {
		Object string `json:"object"` // token_usage response kind
		// Name labels the normalized key subscription after trimming. A blank value
		// selects New API so shared subscription validation receives a nonempty name.
		Name string `json:"name"`
		// TotalGranted, TotalUsed, and TotalAvailable are required finite
		// nonnegative counters for limited keys. Normalization compares
		// TotalGranted against the sum of the other two using CloseEnough, which
		// also rejects overflowed nonfinite sums. Unlimited keys can omit counters
		// or supply finite negative values, which normalization excludes.
		TotalGranted   *float64 `json:"total_granted"`   // reported total raw quota
		TotalUsed      *float64 `json:"total_used"`      // reported consumed raw quota
		TotalAvailable *float64 `json:"total_available"` // reported remaining raw quota
		// UnlimitedQuota is required to select the key's representation. True omits
		// numeric quota limits, while false selects conversion of validated finite counters.
		UnlimitedQuota *bool `json:"unlimited_quota"`
		// ExpiresAt is required and at least minus one in parsed token responses.
		// Nonpositive values mean no expiry; positive seconds or accepted
		// milliseconds become a UTC instant.
		ExpiresAt *int64 `json:"expires_at"`
		// UserBalance and UserBalanceDisplay are alternate embedded wallet sources.
		// UserBalanceDisplay takes precedence and uses display amounts; UserBalance
		// uses raw quota conversion. A malformed present preferred field ends selection.
		UserBalance        json.RawMessage `json:"user_balance"`         // raw embedded wallet quota
		UserBalanceDisplay json.RawMessage `json:"user_balance_display"` // embedded displayed wallet remainder
		// Currency labels embedded display amounts when recognized. Unrecognized
		// text uses the site unit, which Query later checks against the key's unit.
		Currency string `json:"currency"`
	} `json:"data"`
}

// NewAPIWalletBalanceInfo holds one currency's wallet fields in supported JSON
// number or string forms.
type NewAPIWalletBalanceInfo struct {
	// Currency determines whether listed amounts are displayed or raw quota.
	// Recognized units and an absent label mark display values; other text uses
	// the site's quota conversion.
	Currency string `json:"currency"`
	// TotalBalance, Balance, and Remaining are alternate remainder spellings.
	// Selection takes the first nonempty JSON value in that order, and a
	// malformed selected value rejects this wallet entry.
	TotalBalance json.RawMessage `json:"total_balance"` // preferred reported wallet remainder
	Balance      json.RawMessage `json:"balance"`       // alternate reported wallet remainder
	Remaining    json.RawMessage `json:"remaining"`     // alternate remaining wallet amount
	// Used and UsedBalance are optional wallet-usage spellings. UsedBalance is
	// preferred when present; a malformed preferred value rejects the selected
	// entry before shared amount validation.
	Used        json.RawMessage `json:"used"`         // alternate reported wallet consumption
	UsedBalance json.RawMessage `json:"used_balance"` // preferred reported wallet consumption
	Total       json.RawMessage `json:"total"`        // optional reported wallet total
}

// NewAPIWalletData holds a nested wallet or user-self response, retaining raw
// numeric representations for normalization.
type NewAPIWalletData struct {
	// ID identifies the self-query wallet owner when an expected user ID is
	// configured. That path requires a present matching positive integral value
	// before consuming Quota.
	ID json.RawMessage `json:"id"`
	// Quota supplies the self-query remainder and the last generic nested
	// remainder source. Its presence also keeps unlabeled generic amounts on the
	// raw conversion path.
	Quota json.RawMessage `json:"quota"`
	// UsedQuota is a lifetime usage counter. The self-query parser leaves it
	// outside wallet totals, while generic nested parsing uses it when Used is absent.
	UsedQuota json.RawMessage `json:"used_quota"`
	// Balance, Remaining, and TotalBalance are nested remainder alternatives.
	// Generic selection prefers TotalBalance, then Balance, then Remaining,
	// before considering Quota as the final source.
	Balance      json.RawMessage `json:"balance"`       // alternate nested wallet remainder
	Remaining    json.RawMessage `json:"remaining"`     // alternate nested remaining amount
	TotalBalance json.RawMessage `json:"total_balance"` // preferred nested wallet remainder
	Used         json.RawMessage `json:"used"`          // optional reported wallet consumption
	Total        json.RawMessage `json:"total"`         // optional reported wallet total
	// Currency marks recognized nested values as display amounts. An absent label
	// also marks display when Quota is absent; otherwise site quota factors apply.
	Currency string `json:"currency"`
	// BalanceInfos replaces the root list when this nested list is nonempty. The
	// selected list is searched for the site's currency before another available
	// entry is considered.
	BalanceInfos []NewAPIWalletBalanceInfo `json:"balance_infos"`
}

// NewAPIWalletBalanceResponse holds wallet fields at the response root or
// inside a nested data object.
type NewAPIWalletBalanceResponse struct {
	// Code and Success are optional response flags. Either supplied false selects
	// authentication failure before field parsing; absent flags leave wallet
	// shape validation to its selected representation.
	Code         *bool                     `json:"code"`          // optional response validity marker
	Success      *bool                     `json:"success"`       // optional response success marker
	Message      string                    `json:"message"`       // reported upstream diagnostic text
	BalanceInfos []NewAPIWalletBalanceInfo `json:"balance_infos"` // root currency balance entries
	// Currency controls root amount conversion when no list or Data object
	// supplies the wallet. Recognized units or absent text mark display values;
	// other text uses site settings.
	Currency string `json:"currency"`
	// Balance, Remaining, and TotalBalance are root remainder alternatives.
	// TotalBalance takes precedence, then Balance and Remaining, with absent or
	// null JSON skipped before numeric parsing.
	Balance      json.RawMessage `json:"balance"`       // alternate root wallet remainder
	Remaining    json.RawMessage `json:"remaining"`     // alternate root remaining amount
	TotalBalance json.RawMessage `json:"total_balance"` // preferred root wallet remainder
	// Used and UsedBalance are optional root usage observations, with UsedBalance
	// selected first. Optional Total is passed independently to shared amount
	// validation when present.
	Used        json.RawMessage `json:"used"`         // alternate root wallet consumption
	UsedBalance json.RawMessage `json:"used_balance"` // preferred root wallet consumption
	Total       json.RawMessage `json:"total"`        // optional root wallet total
	// Data selects nested wallet fields when a list has not supplied an amount. A
	// present empty object therefore needs its own usable remainder; root amounts
	// do not replace that selected shape.
	Data *NewAPIWalletData `json:"data"`
}

// NewAPIUsageDisplaySettings supplies the site's display unit and raw quota
// conversion factors.
type NewAPIUsageDisplaySettings struct {
	// Unit is the site display unit used by token conversion and wallet matching.
	// TOKENS leaves quota unchanged; CNY selects the additional exchange factor
	// after money conversion.
	Unit string
	// QuotaPerUnit divides raw quota into money units. Nonpositive or nonfinite
	// factors select the shared default before conversion, while TOKENS bypasses the divisor.
	QuotaPerUnit float64
	// USDExchangeRate scales converted money when Unit is CNY. Invalid or
	// nonpositive factors become one; other display units leave this factor unused.
	USDExchangeRate float64
}

// Name returns the registry identifier for combined New API usage queries.
func (*NewAPIUsageAdapter) Name() string { return usageview.UpstreamUsageAdapterNewAPI }

// Query combines a New API user wallet with separately normalized key quota and
// subscription information. It requires a stable request snapshot with usable
// callbacks, and first probes display settings under a shorter budget before
// token and wallet queries use the original context. Supply BaseURL without
// a forced empty query marker so the shared token builder's appended slash
// belongs to the endpoint path.
//
// Wallet discovery follows the fixed source order described by queryWallet. A
// wallet is required even for unlimited keys, because a key's quota cannot
// supply its owner's remaining funds. Nonempty token and wallet units must
// agree before the wallet amount and unit are merged into the result, while key
// limits and subscription observations retain their separate fields.
func (a *NewAPIUsageAdapter) Query(ctx context.Context, input *usagecontract.Request) (*usageview.UpstreamUsageInfo, error) {
	client := usageclient.New(input)
	// Give the optional unit probe its own short budget, then release that timer
	// before required token and wallet requests continue under the query context.
	displayCtx, cancelDisplay := context.WithTimeout(ctx, StatusTimeout)
	display := a.queryDisplaySettings(displayCtx, client)
	cancelDisplay()
	// Return to the query context after the optional display probe, so its short
	// timeout cannot terminate the required token and wallet requests.
	tokenUsage, tokenResponse, tokenErr := a.queryTokenUsage(ctx, client, display)
	if tokenErr != nil {
		return nil, tokenErr
	}
	wallet, walletErr := a.queryWallet(ctx, client, display, tokenResponse)
	if walletErr != nil {
		return nil, walletErr
	}
	if tokenUsage == nil || wallet == nil || wallet.Balance == nil {
		return nil, usageview.ErrUpstreamUsageWalletUnavailable
	}
	if tokenUsage.Unit != "" && wallet.Unit != "" && tokenUsage.Unit != wallet.Unit {
		// A result has one unit. Reject mismatched currencies because labeling key
		// quota with the wallet currency would misstate the amount.
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	// The display has one wallet amount alongside key-specific limits and
	// subscription observations. Merge wallet fields after unit agreement so
	// neither amount is relabeled with another currency.
	tokenUsage.Mode = "balance"
	tokenUsage.Unit = wallet.Unit
	tokenUsage.Balance = wallet.Balance
	return tokenUsage, nil
}

// queryDisplaySettings reads optional site conversion settings, defaulting to
// USD, the default quota divisor, and exchange rate one. Endpoint, request,
// status, or required-shape failures retain those defaults so Query can continue
// its required token and wallet reads.
//
// The response needs success=true and a present QuotaDisplayType even when it
// supplies valid numeric factors. An unrecognized label retains USD while
// positive finite factors can replace defaults. TOKENS retains those factors,
// but quota conversion later bypasses them.
func (a *NewAPIUsageAdapter) queryDisplaySettings(ctx context.Context, client *usageclient.Client) NewAPIUsageDisplaySettings {
	settings := NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: newAPIDefaultQuotaPerUnit, USDExchangeRate: 1}
	endpoint, err := usageclient.UpstreamUsageStatusEndpoint(client.BaseURL)
	if err != nil {
		// The display probe is optional. Retain the conversion defaults when its
		// endpoint cannot be built so required token and wallet requests can proceed.
		return settings
	}
	body, status, err := client.GetURL(ctx, endpoint, false)
	// Failed or unsuccessful display probing leaves the established defaults
	// available, allowing required usage queries to continue despite missing site
	// presentation settings.
	if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices {
		return settings
	}
	var response struct {
		// Success is required and true before this optional probe's values replace
		// conversion defaults; missing or rejected responses retain those defaults.
		Success *bool `json:"success"`
		// Data supplies the site's unit and optional conversion factors. Absence
		// keeps all defaults because no settings payload was observed.
		Data *struct {
			// QuotaDisplayType is required for a usable settings shape. Recognized
			// USD, CNY, or TOKENS text selects the unit; other values retain USD
			// while valid numeric factors can still apply.
			QuotaDisplayType *string `json:"quota_display_type"`
			// QuotaPerUnit replaces the default divisor when it is present, positive,
			// and finite, before later raw quota amounts are converted.
			QuotaPerUnit *float64 `json:"quota_per_unit"`
			// USDExchangeRate replaces factor one when present, positive, and finite.
			// Quota conversion applies this retained factor when the selected display unit is CNY.
			USDExchangeRate *float64 `json:"usd_exchange_rate"`
		} `json:"data"`
	}
	// Unusable display response shape cannot establish site factors. Keep the
	// defaults rather than deriving quota units from a partial or rejected settings object.
	if err := json.Unmarshal(body, &response); err != nil || response.Success == nil || !*response.Success || response.Data == nil || response.Data.QuotaDisplayType == nil {
		return settings
	}
	switch strings.ToUpper(strings.TrimSpace(*response.Data.QuotaDisplayType)) {
	case "USD", "CNY", "TOKENS":
		settings.Unit = strings.ToUpper(strings.TrimSpace(*response.Data.QuotaDisplayType))
	}
	if response.Data.QuotaPerUnit != nil && usageview.ValidPositiveNumber(*response.Data.QuotaPerUnit) {
		settings.QuotaPerUnit = *response.Data.QuotaPerUnit
	}
	if response.Data.USDExchangeRate != nil && usageview.ValidPositiveNumber(*response.Data.USDExchangeRate) {
		settings.USDExchangeRate = *response.Data.USDExchangeRate
	}
	return settings
}

// queryTokenUsage reads the fixed token path through the shared management
// endpoint builder and returns normalized allowance with its decoded wire
// response. Display settings control conversion, and transport, HTTP, parsing,
// or normalization errors stop execution before wallet discovery proceeds.
func (a *NewAPIUsageAdapter) queryTokenUsage(ctx context.Context, client *usageclient.Client, settings NewAPIUsageDisplaySettings) (*usageview.UpstreamUsageInfo, *NewAPITokenUsageResponse, error) {
	endpoint, err := usageclient.UpstreamUsageTokenEndpoint(client.BaseURL)
	if err != nil {
		return nil, nil, err
	}
	body, status, err := client.GetURL(ctx, endpoint, true)
	if err != nil {
		return nil, nil, err
	}
	if httpErr := usageclient.UpstreamUsageHTTPError(status, true); httpErr != nil {
		return nil, nil, httpErr
	}
	response, err := ParseNewAPITokenUsage(body)
	if err != nil {
		return nil, nil, err
	}
	usage, err := NormalizeNewAPITokenUsage(response, settings)
	if err != nil {
		return nil, nil, err
	}
	return usage, response, nil
}

// queryWallet selects a wallet from embedded token fields, a configured
// user-token self query, or the fixed API-key compatibility endpoint, in that
// order. A present embedded field ends selection even when malformed, keeping
// an advertised wallet amount's validation failure visible.
//
// A configured wallet token enables /api/user/self. Authentication failures,
// rate limiting, and deadline failures stop the query; other unsuccessful self
// results allow /user/balance. A transport failure from self is retained and
// can be returned when the compatibility transport also fails. The configured
// user ID is validated before either request and checked against a successful
// self response.
//
// Compatibility success must parse as wallet data; other compatibility statuses
// or shapes return wallet unavailable, with rate limits and deadlines retaining
// their specific errors. Callers do not synthesize wallet funds from the token
// allowance when no wallet source succeeds.
func (a *NewAPIUsageAdapter) queryWallet(
	ctx context.Context,
	client *usageclient.Client,
	settings NewAPIUsageDisplaySettings,
	tokenResponse *NewAPITokenUsageResponse,
) (*usageview.UpstreamUsageInfo, error) {
	// A present embedded amount owns the selected wallet source. Return its
	// validation error directly so a malformed preferred value cannot silently
	// select another account endpoint.
	if embedded, present, err := NormalizeNewAPITokenWallet(tokenResponse, settings); present {
		if err != nil {
			return nil, err
		}
		return embedded, nil
	}

	userToken := strings.TrimSpace(client.WalletToken)
	userID, err := ConfiguredNewAPIUserID(client.WalletUserID)
	if err != nil {
		return nil, err
	}
	var lastRequestErr error
	// The configured user credential selects the self-query route first.
	// Authentication and deadline failures end this path, while other failed
	// self results permit the fixed compatibility request.
	if userToken != "" {
		endpoint, endpointErr := usageclient.UpstreamUsageUserSelfEndpoint(client.BaseURL)
		if endpointErr != nil {
			return nil, endpointErr
		}
		body, status, requestErr := client.GetURLWithBearer(ctx, endpoint, userToken, userID)
		if requestErr == nil {
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				return nil, usageview.ErrUpstreamUsageWalletAuthFailed
			}
			if status == http.StatusTooManyRequests {
				return nil, usageview.ErrUpstreamUsageRateLimited
			}
			if status >= http.StatusOK && status < http.StatusMultipleChoices {
				wallet, parseErr := ParseNewAPIUserSelfWallet(body, settings, userID)
				if parseErr == nil {
					return wallet, nil
				}
				if errors.Is(parseErr, usageview.ErrUpstreamUsageAuthFailed) || errors.Is(parseErr, usageview.ErrUpstreamUsageWalletAuthFailed) {
					return nil, usageview.ErrUpstreamUsageWalletAuthFailed
				}
			}
		} else if errors.Is(requestErr, usageview.ErrUpstreamUsageTimeout) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, requestErr
		} else if requestErr != nil {
			// Retain a non-deadline self-query transport failure while trying the
			// next wallet route, so a second transport failure can report the earlier
			// request's evidence.
			lastRequestErr = requestErr
		}
	}

	// Try the fixed API-key wallet route after self-query did not supply usable
	// data. A missing route or HTML body cannot establish remaining funds and is
	// classified as wallet unavailable.
	endpoint, err := usageclient.UpstreamUsageWalletEndpoint(client.BaseURL)
	if err != nil {
		return nil, err
	}
	body, status, requestErr := client.GetURLWithBearer(ctx, endpoint, client.APIKey, "")
	if requestErr != nil {
		if errors.Is(requestErr, usageview.ErrUpstreamUsageTimeout) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, requestErr
		}
		// If both wallet transports failed, expose the retained self-query failure.
		// The compatibility failure otherwise becomes wallet unavailable when no
		// usable amount was observed.
		if lastRequestErr != nil {
			return nil, lastRequestErr
		}
		return nil, usageview.ErrUpstreamUsageWalletUnavailable
	}
	if status == http.StatusTooManyRequests {
		return nil, usageview.ErrUpstreamUsageRateLimited
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		wallet, parseErr := ParseNewAPIWalletBalance(body, settings)
		if parseErr == nil {
			return wallet, nil
		}
	}
	return nil, usageview.ErrUpstreamUsageWalletUnavailable
}

// NormalizeNewAPITokenWallet reads an embedded wallet and reports whether a
// field was present, even when that field is invalid. Missing response data or
// both absent/null wallet fields return present=false, allowing queryWallet to
// try a separate request.
//
// UserBalanceDisplay takes precedence and already uses a display amount;
// UserBalance instead converts raw quota using site settings. A malformed
// preferred value rejects the response rather than selecting its lower-priority
// counterpart. Normalized wallet validation retains finite negative debt while
// rejecting unusable numbers.
func NormalizeNewAPITokenWallet(response *NewAPITokenUsageResponse, settings NewAPIUsageDisplaySettings) (*usageview.UpstreamUsageInfo, bool, error) {
	if response == nil || response.Data == nil {
		return nil, false, nil
	}
	data := response.Data
	if raw := NonEmptyJSON(data.UserBalanceDisplay); raw != nil {
		value, err := ParseNewAPINumber(raw)
		if err != nil || value == nil {
			return nil, true, usageview.ErrUpstreamUsageInvalidResponse
		}
		unit, _ := NormalizeNewAPIWalletUnit(data.Currency, settings.Unit)
		usage, usageErr := NewAPIWalletAmountUsage(unit, nil, nil, value)
		return usage, true, usageErr
	}
	if raw := NonEmptyJSON(data.UserBalance); raw != nil {
		value, err := ParseNewAPINumber(raw)
		if err != nil || value == nil || !usageview.ValidFiniteNumber(*value) {
			return nil, true, usageview.ErrUpstreamUsageInvalidResponse
		}
		remaining := NormalizeNewAPIQuota(*value, settings)
		usage, usageErr := NewAPIWalletAmountUsage(settings.Unit, nil, nil, &remaining)
		return usage, true, usageErr
	}
	return nil, false, nil
}

// ParseNewAPIWalletBalance selects and normalizes one wallet amount from a JSON
// envelope. A nonempty nested balance_infos list replaces the root list;
// multiple entries first prefer the site's unit, then use the first available
// entry when no preferred unit was selected.
//
// Within an entry, TotalBalance precedes Balance and Remaining. With no list, a
// supplied Data object selects nested fields, otherwise root fields are used.
// Nested Quota is the last remaining source. Recognized currencies mark display
// amounts; absent currency also marks list and root values as display, and
// nested values as display when Quota is absent.
//
// Raw quota amounts are converted with site settings. Required remaining and
// optional used/total values must parse and pass shared amount validation;
// malformed preferred sources reject the selected wallet. Present false Code or
// Success flags produce an authentication error.
func ParseNewAPIWalletBalance(body []byte, settings NewAPIUsageDisplaySettings) (*usageview.UpstreamUsageInfo, error) {
	var response NewAPIWalletBalanceResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	if response.Code != nil && !*response.Code || response.Success != nil && !*response.Success {
		return nil, usageview.ErrUpstreamUsageAuthFailed
	}
	infos := response.BalanceInfos
	if response.Data != nil && len(response.Data.BalanceInfos) > 0 {
		infos = response.Data.BalanceInfos
	}
	// Prefer the site's currency across wallet entries before accepting another
	// unit, keeping a later matching entry from being hidden by response order.
	for pass := range 2 {
		for _, info := range infos {
			unit, displayValue := NormalizeNewAPIWalletUnit(info.Currency, settings.Unit)
			// balance_infos amounts already use display units when currency is absent.
			// Dividing again by quota_per_unit would understate the wallet balance.
			if strings.TrimSpace(info.Currency) == "" {
				displayValue = true
			}
			if len(infos) > 1 && pass == 0 && settings.Unit != "" && unit != settings.Unit {
				continue
			}
			remainingRaw := FirstNewAPIRaw(info.TotalBalance, info.Balance, info.Remaining)
			remaining, err := ParseNewAPINumber(remainingRaw)
			if err != nil || remaining == nil {
				return nil, usageview.ErrUpstreamUsageInvalidResponse
			}
			if !displayValue {
				value := NormalizeNewAPIQuota(*remaining, settings)
				remaining = &value
			}
			used, err := ParseNewAPINumber(FirstNewAPIRaw(info.UsedBalance, info.Used))
			if err != nil {
				return nil, usageview.ErrUpstreamUsageInvalidResponse
			}
			total, err := ParseNewAPINumber(info.Total)
			if err != nil {
				return nil, usageview.ErrUpstreamUsageInvalidResponse
			}
			if displayValue {
				if used != nil {
					usedValue := *used
					used = &usedValue
				}
				if total != nil {
					totalValue := *total
					total = &totalValue
				}
			} else {
				if used != nil {
					usedValue := NormalizeNewAPIQuota(*used, settings)
					used = &usedValue
				}
				if total != nil {
					totalValue := NormalizeNewAPIQuota(*total, settings)
					total = &totalValue
				}
			}
			return NewAPIWalletAmountUsage(unit, used, total, remaining)
		}
	}
	// When no nested data object is supplied, select root wallet fields using
	// the same amount precedence as list entries; a supplied object selects nested handling.
	if response.Data == nil {
		unit, displayValue := NormalizeNewAPIWalletUnit(response.Currency, settings.Unit)
		remainingRaw := FirstNewAPIRaw(response.TotalBalance, response.Balance, response.Remaining)
		// Root wallet amounts with no currency label are treated as display values,
		// so an already-displayed balance is not divided by the raw quota factor.
		if strings.TrimSpace(response.Currency) == "" {
			displayValue = true
		}
		remaining, err := ParseNewAPINumber(remainingRaw)
		if err != nil || remaining == nil {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		used, err := ParseNewAPINumber(FirstNewAPIRaw(response.UsedBalance, response.Used))
		if err != nil {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		total, err := ParseNewAPINumber(response.Total)
		if err != nil {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		if !displayValue {
			remainingValue := NormalizeNewAPIQuota(*remaining, settings)
			remaining = &remainingValue
			if used != nil {
				usedValue := NormalizeNewAPIQuota(*used, settings)
				used = &usedValue
			}
			if total != nil {
				totalValue := NormalizeNewAPIQuota(*total, settings)
				total = &totalValue
			}
		}
		return NewAPIWalletAmountUsage(unit, used, total, remaining)
	}
	data := response.Data
	unit, displayValue := NormalizeNewAPIWalletUnit(data.Currency, settings.Unit)
	remainingRaw := FirstNewAPIRaw(data.TotalBalance, data.Balance, data.Remaining, data.Quota)
	// A nested amount without currency or Quota is a display value. Presence of
	// Quota keeps the raw conversion path selected even if another amount field wins precedence.
	if strings.TrimSpace(data.Currency) == "" && NonEmptyJSON(data.Quota) == nil {
		displayValue = true
	}
	remaining, err := ParseNewAPINumber(remainingRaw)
	if err != nil || remaining == nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	used, err := ParseNewAPINumber(FirstNewAPIRaw(data.Used, data.UsedQuota))
	if err != nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	total, err := ParseNewAPINumber(data.Total)
	if err != nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	if !displayValue {
		remainingValue := NormalizeNewAPIQuota(*remaining, settings)
		remaining = &remainingValue
		if used != nil {
			usedValue := NormalizeNewAPIQuota(*used, settings)
			used = &usedValue
		}
		if total != nil {
			totalValue := NormalizeNewAPIQuota(*total, settings)
			total = &totalValue
		}
	}
	return NewAPIWalletAmountUsage(unit, used, total, remaining)
}

// ParseNewAPIUserSelfWallet validates a nested user-self wallet and returns its
// quota-derived remainder. False success/code flags reject authentication, and
// a nonempty expectedUserID requires a present ID whose normalized integer text
// matches that supplied identity.
//
// Quota is the required amount, converted unless a recognized currency marks it
// as display units. Lifetime UsedQuota is left outside the wallet amount, so
// the result contains remaining funds without inventing a total from an
// unrelated lifetime counter. Shared validation permits finite negative
// remaining values.
func ParseNewAPIUserSelfWallet(body []byte, settings NewAPIUsageDisplaySettings, expectedUserID string) (*usageview.UpstreamUsageInfo, error) {
	var response NewAPIWalletBalanceResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	if response.Success != nil && !*response.Success || response.Code != nil && !*response.Code {
		return nil, usageview.ErrUpstreamUsageAuthFailed
	}
	if response.Data == nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	if expectedUserID != "" {
		if NonEmptyJSON(response.Data.ID) == nil {
			return nil, usageview.ErrUpstreamUsageWalletAuthFailed
		}
		actual, err := ParseNewAPIInteger(response.Data.ID)
		if err != nil || actual != expectedUserID {
			return nil, usageview.ErrUpstreamUsageWalletAuthFailed
		}
	}
	remaining, err := ParseNewAPINumber(response.Data.Quota)
	if err != nil || remaining == nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	unit, displayValue := NormalizeNewAPIWalletUnit(response.Data.Currency, settings.Unit)
	if !displayValue {
		remainingValue := NormalizeNewAPIQuota(*remaining, settings)
		remaining = &remainingValue
	}
	// The user-self lifetime counter has no matching wallet period total. Pass
	// only the observed remainder so the display cannot combine lifetime usage
	// with current funds into a synthetic wallet capacity.
	return NewAPIWalletAmountUsage(unit, nil, nil, remaining)
}

// NewAPIWalletAmountUsage validates observed wallet components and retains the
// supplied numeric pointers in a balance result. At least one component must be
// present; callers keep pointed values stable while consuming the result and
// validate the display unit separately.
func NewAPIWalletAmountUsage(unit string, used, total, remaining *float64) (*usageview.UpstreamUsageInfo, error) {
	amount := &usageview.UpstreamUsageAmount{Used: used, Total: total, Remaining: remaining}
	if err := usageview.ValidateUsageAmount(amount); err != nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	return &usageview.UpstreamUsageInfo{Provider: usageview.UpstreamUsageAdapterNewAPI, Mode: "balance", Unit: unit, Balance: amount}, nil
}

// ConfiguredNewAPIUserID trims and canonicalizes a positive decimal int64
// identifier, or returns empty for an unconfigured value. Invalid, overflowing,
// and nonpositive values return a configuration error before user-specific
// wallet requests are sent.
func ConfiguredNewAPIUserID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return "", usageview.ErrUpstreamUsageConfigInvalid
	}
	return strconv.FormatInt(value, 10), nil
}

// ParseNewAPINumber decodes a finite JSON number or numeric string into
// float64, returning nil for missing or null input. Strings permit trimmed
// whitespace, commas, and leading dollar or yuan symbols; malformed or
// nonfinite values return an error.
func ParseNewAPINumber(raw json.RawMessage) (*float64, error) {
	raw = NonEmptyJSON(raw)
	if raw == nil {
		return nil, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		if !usageview.ValidFiniteNumber(value) {
			return nil, errors.New("invalid numeric value")
		}
		return &value, nil
	}
	var textValue string
	if err := json.Unmarshal(raw, &textValue); err != nil {
		return nil, err
	}
	textValue = strings.TrimSpace(textValue)
	// Normalize supported display-number spellings before float parsing, letting
	// numeric wallet strings include grouping separators and leading currency
	// symbols used by these response formats.
	textValue = strings.ReplaceAll(textValue, ",", "")
	textValue = strings.TrimPrefix(textValue, "$")
	textValue = strings.TrimPrefix(textValue, "¥")
	value, err := strconv.ParseFloat(textValue, 64)
	if err != nil || !usageview.ValidFiniteNumber(value) {
		return nil, errors.New("invalid numeric value")
	}
	return &value, nil
}

// ParseNewAPIInteger formats the int64 conversion of a positive integral
// float64 decoded by ParseNewAPINumber. Missing, nonpositive, and nonintegral
// decoded values fail. Float64 rounding can make a fractional input integral
// before this check, and the conversion does not validate the int64 range.
func ParseNewAPIInteger(raw json.RawMessage) (string, error) {
	value, err := ParseNewAPINumber(raw)
	if err != nil || value == nil || *value <= 0 || math.Trunc(*value) != *value {
		return "", errors.New("invalid user id")
	}
	// Formatting follows a float64-to-int64 conversion after integrality checks;
	// precision loss and out-of-range conversion remain possible for very large IDs.
	return strconv.FormatInt(int64(*value), 10), nil
}

// NonEmptyJSON returns raw unless it is absent, whitespace-only, or JSON null.
// Returned bytes borrow the supplied storage, and other JSON spellings remain
// available for the selected field's parser.
func NonEmptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	return raw
}

// FirstNewAPIRaw returns the first supplied value that NonEmptyJSON retains, or
// nil when all are absent. The bytes borrow that value's storage; callers
// validate its content after source selection.
func FirstNewAPIRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if raw := NonEmptyJSON(value); raw != nil {
			return raw
		}
	}
	return nil
}

// NormalizeNewAPIWalletUnit recognizes supported currency aliases and reports
// whether amounts already use display units. Unrecognized text selects
// fallback, or USD when fallback is empty, with false directing callers to
// quota conversion.
func NormalizeNewAPIWalletUnit(raw, fallback string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "USD", "$", "US$":
		return "USD", true
	case "CNY", "RMB", "¥", "￥":
		return "CNY", true
	case "TOKENS", "TOKEN":
		return "TOKENS", true
	}
	if fallback == "" {
		return "USD", false
	}
	return fallback, false
}

// ParseNewAPITokenUsage decodes the token envelope and distinguishes a rejected
// key from malformed usage. Present false Code or Success flags fail
// authentication first, while accepted data requires Code=true, token_usage
// identity, an unlimited flag, and an expiry of at least minus one.
//
// Unlimited keys allow absent or finite negative counters because normalization
// omits quota amounts for that representation. Limited keys require all three
// finite nonnegative counters; NormalizeNewAPITokenUsage subsequently checks
// their consistency with CloseEnough, rejecting overflowed nonfinite sums
// before displaying key capacity. Wallet fields stay raw for separate source
// selection.
func ParseNewAPITokenUsage(body []byte) (*NewAPITokenUsageResponse, error) {
	var response NewAPITokenUsageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	// An explicit response-level rejection determines authentication failure
	// before shape checks, so a rejected key cannot become a misleading
	// missing-field diagnostic.
	if response.Code != nil && !*response.Code || response.Success != nil && !*response.Success {
		return nil, usageview.ErrUpstreamUsageAuthFailed
	}
	if response.Code == nil || !*response.Code || response.Data == nil ||
		response.Data.Object != "token_usage" || response.Data.UnlimitedQuota == nil || response.Data.ExpiresAt == nil ||
		*response.Data.ExpiresAt < -1 {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	if *response.Data.UnlimitedQuota {
		// Unlimited normalization omits quota counters. Accept finite negative
		// values in this representation so counter sentinel values cannot turn an
		// otherwise valid unlimited key into displayed negative capacity.
		for _, value := range []*float64{response.Data.TotalGranted, response.Data.TotalUsed, response.Data.TotalAvailable} {
			if value != nil && !usageview.ValidFiniteNumber(*value) {
				return nil, usageview.ErrUpstreamUsageInvalidResponse
			}
		}
		return &response, nil
	}
	if response.Data.TotalGranted == nil || response.Data.TotalUsed == nil || response.Data.TotalAvailable == nil ||
		!usageview.ValidNonNegativeNumber(*response.Data.TotalGranted) || !usageview.ValidNonNegativeNumber(*response.Data.TotalUsed) ||
		!usageview.ValidNonNegativeNumber(*response.Data.TotalAvailable) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	return &response, nil
}

// NormalizeNewAPITokenUsage converts a parsed token response into key limits or
// an unlimited subscription. Callers supply the required pointers and finite
// counters established by ParseNewAPITokenUsage; nil response data is rejected,
// and expiry is normalized separately.
//
// Unlimited quota omits numeric capacity observations. Limited quota requires
// TotalGranted to pass CloseEnough against TotalUsed plus TotalAvailable,
// rejecting nonfinite sums before converting all three values with the same
// display settings. The token_quota limit and subscription remainder describe
// key allowance; Query adds an independently selected wallet later.
func NormalizeNewAPITokenUsage(response *NewAPITokenUsageResponse, settings NewAPIUsageDisplaySettings) (*usageview.UpstreamUsageInfo, error) {
	if response == nil || response.Data == nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	expiresAt, err := NormalizeNewAPITime(*response.Data.ExpiresAt)
	if err != nil {
		return nil, err
	}
	planName := strings.TrimSpace(response.Data.Name)
	if planName == "" {
		planName = "New API"
	}
	// Represent unlimited key allowance with its flag and expiry, omitting
	// numeric counters so negative sentinels cannot enter displayed token quota or wallet funds.
	if *response.Data.UnlimitedQuota {
		return &usageview.UpstreamUsageInfo{
			Provider: usageview.UpstreamUsageAdapterNewAPI,
			Mode:     "subscription",
			Unit:     settings.Unit,
			Subscription: &usageview.UpstreamUsageSubscription{
				PlanName:  planName,
				Unlimited: true,
				ExpiresAt: expiresAt,
			},
			ExpiresAt: expiresAt,
		}, nil
	}
	// Finite source counters can overflow when added; CloseEnough rejects any
	// nonfinite sum before display conversion proceeds.
	if !CloseEnough(*response.Data.TotalGranted, *response.Data.TotalUsed+*response.Data.TotalAvailable) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	used := NormalizeNewAPIQuota(*response.Data.TotalUsed, settings)
	total := NormalizeNewAPIQuota(*response.Data.TotalGranted, settings)
	remaining := NormalizeNewAPIQuota(*response.Data.TotalAvailable, settings)
	limit := usageview.UpstreamUsageLimit{Name: "token_quota", Used: &used, Limit: &total, Remaining: &remaining}
	return &usageview.UpstreamUsageInfo{
		Provider: usageview.UpstreamUsageAdapterNewAPI,
		Mode:     "limits",
		Unit:     settings.Unit,
		Limits:   []usageview.UpstreamUsageLimit{limit},
		Subscription: &usageview.UpstreamUsageSubscription{
			PlanName:  planName,
			Remaining: &remaining,
			ExpiresAt: expiresAt,
		},
		ExpiresAt: expiresAt,
	}, nil
}

// NormalizeNewAPIQuota converts raw quota to the site's display unit, leaving
// TOKENS unchanged. Money units divide by a positive finite quota factor, with
// unusable factors selecting the shared default.
//
// CNY additionally multiplies by a positive finite exchange rate, or one when
// that factor is unusable. The input itself is not validated here, and extreme
// factors can make a finite input produce a nonfinite result. Callers validate
// derived wallet or token quantities before publishing them.
func NormalizeNewAPIQuota(value float64, settings NewAPIUsageDisplaySettings) float64 {
	if settings.Unit == "TOKENS" {
		return value
	}
	divisor := settings.QuotaPerUnit
	if !usageview.ValidPositiveNumber(divisor) {
		divisor = newAPIDefaultQuotaPerUnit
	}
	// Scale raw quota to the site's money display unit.
	converted := value / divisor
	if settings.Unit == "CNY" {
		rate := settings.USDExchangeRate
		if !usageview.ValidPositiveNumber(rate) {
			rate = 1
		}
		// Apply the configured exchange factor to the CNY display amount.
		converted *= rate
	}
	return converted
}

// NormalizeNewAPITime returns a UTC expiry from a positive epoch, with
// nonpositive values treated as absent. Values beyond the final second of year
// 9999 are interpreted as milliseconds up to the corresponding ceiling,
// truncating subsecond precision; larger values are rejected.
func NormalizeNewAPITime(value int64) (*time.Time, error) {
	if value <= 0 {
		return nil, nil
	}
	// Values above the last valid year-9999 second use the millisecond path.
	// Check its corresponding ceiling before truncating to seconds for the displayed expiry.
	if value > 253402300799 {
		if value > 253402300799000 {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		value /= 1000
	}
	result := time.Unix(value, 0).UTC()
	if result.IsZero() {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	return &result, nil
}
