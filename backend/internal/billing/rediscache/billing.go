package rediscache

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// billing.go caches user balances and API-key spending windows in Redis.
// Balance and API Key spending caches receive post-settlement amount updates
// while PostgreSQL remains the fund record. Missing keys stay absent during
// Lua updates, allowing later reads to rebuild their snapshots. Balance
// entries use subtractive expiry jitter, and every successful subtraction
// refreshes a whole-second lease.
//
// The spending hash stores amounts and Unix-second starts for three windows.
// Lua resets missing/expired starts to the supplied current second and cost;
// postgres.KeyUsageStore.IncrementRateLimitUsage instead adds cost to prior
// usage at a missing start and aligns daily/seven-day starts to database-session
// midnight. These cache updates are calculated and written in one Lua execution,
// while initial hash writes pipeline the value and expiry commands separately.

const (
	// Billing key prefixes separate user balance strings from API Key spending
	// hashes so each snapshot can expire or invalidate by its own identity.
	billingBalanceKeyPrefix   = "billing:balance:"
	billingRateLimitKeyPrefix = "apikey:rate:"
	// billingCacheTTL and billingCacheJitter define subtractive balance
	// expiration spreading. go-redis SET encodes the duration as EX seconds or PX
	// milliseconds, while Lua deduction truncates it to whole seconds.
	billingCacheTTL    = 5 * time.Minute
	billingCacheJitter = 30 * time.Second
	// rateLimitCacheTTL retains spending hashes for the longest cached window,
	// seven days. Each hash snapshot or successful usage update refreshes that
	// lease.
	rateLimitCacheTTL = 7 * 24 * time.Hour // 7 days matches the longest window

	// Spending window durations mirror the core five-hour, one-day and seven-day
	// checks. Lua receives them in seconds for its stored Unix-start comparisons.
	rateLimitWindow5h = 5 * time.Hour
	rateLimitWindow1d = 24 * time.Hour
	rateLimitWindow7d = 7 * 24 * time.Hour

	// Hash field names pair usage amounts with Unix-second starts for the three
	// spending windows, shared by snapshot encoding and Lua mutation.
	rateLimitFieldUsage5h  = "usage_5h"
	rateLimitFieldUsage1d  = "usage_1d"
	rateLimitFieldUsage7d  = "usage_7d"
	rateLimitFieldWindow5h = "window_5h"
	rateLimitFieldWindow1d = "window_1d"
	rateLimitFieldWindow7d = "window_7d"
)

var (
	// deductBalanceScript subtracts from an existing numeric balance and replaces
	// its expiry within one execution. Missing strings remain absent so
	// eligibility can refill them later.
	deductBalanceScript = redis.NewScript(`
		local current = redis.call('GET', KEYS[1])
		if current == false then
			return 0
		end
		local newVal = tonumber(current) - tonumber(ARGV[1])
		redis.call('SET', KEYS[1], newVal)
		redis.call('EXPIRE', KEYS[1], ARGV[2])
		return 1
	`)

	// updateRateLimitUsageScript updates existing hash windows from cost and
	// Unix-second arguments. Zero or expired starts reset to the supplied second
	// and cost; active windows add cost, and the hash expiry is refreshed.
	//
	// Its six arguments are cost, TTL seconds, now Unix seconds, then the three
	// window durations in seconds. Missing hashes stay absent. Compared with
	// postgres.KeyUsageStore.IncrementRateLimitUsage, Lua resets usage at missing
	// starts and uses current seconds for daily/seven-day anchors.
	updateRateLimitUsageScript = redis.NewScript(`
		local exists = redis.call('EXISTS', KEYS[1])
		if exists == 0 then
			return 0
		end
		local cost = tonumber(ARGV[1])
		local now = tonumber(ARGV[3])
		local win5h = tonumber(ARGV[4])
		local win1d = tonumber(ARGV[5])
		local win7d = tonumber(ARGV[6])

		-- Helper: check if window is expired and update usage + window accordingly
		-- Returns nothing, modifies the hash in-place.
		local function update_window(usage_field, window_field, window_duration)
			local w = tonumber(redis.call('HGET', KEYS[1], window_field) or 0)
			if w == 0 or (now - w) >= window_duration then
				-- Window expired or never started: reset usage to cost, start new window
				redis.call('HSET', KEYS[1], usage_field, tostring(cost))
				redis.call('HSET', KEYS[1], window_field, tostring(now))
			else
				-- Window still valid: accumulate
				redis.call('HINCRBYFLOAT', KEYS[1], usage_field, cost)
			end
		end

		update_window('usage_5h', 'window_5h', win5h)
		update_window('usage_1d', 'window_1d', win1d)
		update_window('usage_7d', 'window_7d', win7d)
		redis.call('EXPIRE', KEYS[1], ARGV[2])
		return 1
	`)
)

// Cache stores billing balance and API Key spending snapshots in a
// caller-owned Redis client.
type Cache struct {
	// rdb is retained from the caller and supplies every cache command. The
	// owning application keeps it open and closes it after cache consumers stop.
	rdb *redis.Client // shared billing Redis client
}

// jitteredTTL returns five minutes minus a random duration in [0, thirty
// seconds). Nonpositive configured jitter would return the base lifetime;
// normal values therefore stay above four minutes thirty seconds and at most
// five minutes.
func jitteredTTL() time.Duration {
	// Subtract the sampled duration to spread reloads while keeping every
	// returned lifetime at or below the configured balance-cache base.
	if billingCacheJitter <= 0 {
		return billingCacheTTL
	}
	jitter := time.Duration(rand.IntN(int(billingCacheJitter)))
	return billingCacheTTL - jitter
}

// billingBalanceKey returns the balance namespace plus the supplied decimal
// user ID.
func billingBalanceKey(userID int64) string {
	return fmt.Sprintf("%s%d", billingBalanceKeyPrefix, userID)
}

// billingRateLimitKey returns the spending namespace plus the supplied decimal
// API Key ID.
func billingRateLimitKey(keyID int64) string {
	return fmt.Sprintf("%s%d", billingRateLimitKeyPrefix, keyID)
}

// NewBillingCache binds a caller-owned Redis client without reading or warming
// snapshots. Methods require a usable client and operation context; missing
// keys are handled by each balance or spending method's own result rule.
func NewBillingCache(rdb *redis.Client) *Cache {
	return &Cache{rdb: rdb}
}

// GetUserBalance returns the decoded numeric balance or its Redis/parse error.
// A missing entry returns redis.Nil, allowing eligibility to reload persisted
// funds; this read does not refresh the stored expiry.
func (c *Cache) GetUserBalance(ctx context.Context, userID int64) (float64, error) {
	key := billingBalanceKey(userID)
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(val, 64)
}

// SetUserBalance stores the supplied balance with a jittered expiration using
// one Redis SET. The caller supplies a finite cache amount; existing data is
// replaced and the caller owns the Redis client lifecycle.
func (c *Cache) SetUserBalance(ctx context.Context, userID int64, balance float64) error {
	key := billingBalanceKey(userID)
	return c.rdb.Set(ctx, key, balance, jitteredTTL()).Err()
}

// DeductUserBalance subtracts amount from an existing balance and refreshes
// its jittered whole-second TTL in one Lua execution. A missing key remains
// absent and succeeds; redis.Nil is suppressed, while other errors are logged
// and returned. The caller supplies a finite amount and coordinates repeated
// updates.
func (c *Cache) DeductUserBalance(ctx context.Context, userID int64, amount float64) error {
	key := billingBalanceKey(userID)
	// Lua uses whole-second expiry for this mutation, while go-redis SET can
	// encode jittered durations in milliseconds through PX.
	_, err := deductBalanceScript.Run(ctx, c.rdb, []string{key}, amount, int(jitteredTTL().Seconds())).Result()
	// Missing-key results leave refill responsibility with eligibility; report
	// other command failures because this cached deduction may not have completed.
	if err != nil && !errors.Is(err, redis.Nil) {
		logging.LegacyPrintf("cache.billing", "Warning: deduct balance cache failed for user %d: %v", userID, err)
		return err
	}
	return nil
}

// InvalidateUserBalance deletes the user balance snapshot through the supplied
// context. Missing keys succeed, while Redis errors return; the next
// eligibility lookup can then rebuild the amount from persistence.
func (c *Cache) InvalidateUserBalance(ctx context.Context, userID int64) error {
	key := billingBalanceKey(userID)
	// Delete the cached amount so a later Redis miss can drive eligibility back
	// to persistent user fund data for its next balance observation.
	return c.rdb.Del(ctx, key).Err()
}

// GetAPIKeyRateLimit reads USD usage and Unix-second window starts from the
// key hash. An empty hash yields redis.Nil; missing fields retain zero, and
// parsed values are kept even when decoding reports an error. Eligibility
// converts positive starts to times and evaluates window expiry and usage
// ceilings; that path adds no check that usage is finite or nonnegative.
func (c *Cache) GetAPIKeyRateLimit(ctx context.Context, keyID int64) (*billing.APIKeyRateLimitCacheData, error) {
	key := billingRateLimitKey(keyID)
	result, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, redis.Nil
	}
	data := &billing.APIKeyRateLimitCacheData{}
	// Syntax errors supply zero. Range errors retain signed integer limits or
	// float infinities, and accepted NaN/Inf usage text also reaches eligibility
	// unchanged.
	if v, ok := result[rateLimitFieldUsage5h]; ok {
		data.Usage5h, _ = strconv.ParseFloat(v, 64)
	}
	if v, ok := result[rateLimitFieldUsage1d]; ok {
		data.Usage1d, _ = strconv.ParseFloat(v, 64)
	}
	if v, ok := result[rateLimitFieldUsage7d]; ok {
		data.Usage7d, _ = strconv.ParseFloat(v, 64)
	}
	if v, ok := result[rateLimitFieldWindow5h]; ok {
		data.Window5h, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := result[rateLimitFieldWindow1d]; ok {
		data.Window1d, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := result[rateLimitFieldWindow7d]; ok {
		data.Window7d, _ = strconv.ParseInt(v, 10, 64)
	}
	return data, nil
}

// SetAPIKeyRateLimit writes a non-nil spending snapshot and its seven-day
// expiry through a pipeline. Nil input leaves the stored key unchanged and
// succeeds. HSET and EXPIRE are separate commands, so an error does not
// establish that neither operation occurred.
func (c *Cache) SetAPIKeyRateLimit(ctx context.Context, keyID int64, data *billing.APIKeyRateLimitCacheData) error {
	if data == nil {
		return nil
	}
	key := billingRateLimitKey(keyID)
	fields := map[string]any{
		rateLimitFieldUsage5h:  data.Usage5h,
		rateLimitFieldUsage1d:  data.Usage1d,
		rateLimitFieldUsage7d:  data.Usage7d,
		rateLimitFieldWindow5h: data.Window5h,
		rateLimitFieldWindow1d: data.Window1d,
		rateLimitFieldWindow7d: data.Window7d,
	}
	// Queue snapshot fields before expiry for one pipeline send; the commands are
	// not a Redis transaction and may leave partial effects after failure.
	pipe := c.rdb.Pipeline()
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, rateLimitCacheTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// UpdateAPIKeyRateLimitUsage updates three spending windows using current Unix
// seconds in one Lua execution. The caller supplies a finite cost; a missing
// hash remains absent, redis.Nil is suppressed, and other errors are logged
// and returned.
//
// A zero or elapsed start resets that window to this cost and records the
// current second; other windows add to their previous usage. Successful
// existing-hash updates refresh the seven-day expiry. Daily/seven-day cache starts
// remain the supplied second, so this path does not implement PostgreSQL
// midnight alignment.
func (c *Cache) UpdateAPIKeyRateLimitUsage(ctx context.Context, keyID int64, cost float64) error {
	key := billingRateLimitKey(keyID)
	// The script stores seconds so this observation and all duration arguments
	// use the same unit for its missing/elapsed start decisions.
	now := time.Now().Unix()
	_, err := updateRateLimitUsageScript.Run(ctx, c.rdb, []string{key},
		cost,
		int(rateLimitCacheTTL.Seconds()),
		now,
		int(rateLimitWindow5h.Seconds()),
		int(rateLimitWindow1d.Seconds()),
		int(rateLimitWindow7d.Seconds()),
	).Result()
	// Missing-hash results leave refill responsibility with eligibility; report
	// other command failures because this spending-cache update may not have completed.
	if err != nil && !errors.Is(err, redis.Nil) {
		logging.LegacyPrintf("cache.billing", "Warning: update rate limit usage cache failed for api key %d: %v", keyID, err)
		return err
	}
	return nil
}

// InvalidateAPIKeyRateLimit deletes the key spending snapshot through the
// supplied context. Missing hashes succeed, while Redis errors return; a later
// eligibility lookup can refill amounts and window starts from persistence.
func (c *Cache) InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error {
	key := billingRateLimitKey(keyID)
	// Delete cached spending fields so a later Redis miss lets eligibility refill
	// usage and window starts from its database source.
	return c.rdb.Del(ctx, key).Err()
}
