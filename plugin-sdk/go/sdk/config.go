package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sync/atomic"
)

// config.go exposes immutable configuration snapshots through a stable handle.
// The runtime replaces the handle's pointer after decoding a complete JSON
// object, allowing HTTP handlers to read configuration concurrently with host
// updates. Readers never receive the underlying map and cannot modify the
// values used by other handlers. A zero Config behaves like an empty object.
//
// Env.Config retains the same handle throughout a process generation. A
// ConfigChanged callback receives a separate snapshot of the new values, so
// later updates cannot change the argument being processed by that callback.
// Accessors return their zero value for absent or differently typed fields;
// manifest validation and host normalization own required-field enforcement.

// Config provides concurrent scalar reads from a host-validated configuration.
type Config struct {
	// current points to immutable values and is shared only by copies of the same live configuration handle.
	current *atomic.Pointer[configValues]
}

// configValues stores one immutable map after complete JSON decoding.
type configValues struct {
	values map[string]any // immutable decoded configuration values
}

// NewConfig parses a configuration object into a separately owned live handle.
// JSON numbers retain their textual representation until a typed accessor reads
// them. Nonobject JSON and malformed values return an error.
func NewConfig(data []byte) (Config, error) {
	var values map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if !json.Valid(data) {
		return Config{}, fmt.Errorf("configuration must be valid JSON")
	}
	if err := decoder.Decode(&values); err != nil {
		return Config{}, err
	}
	if values == nil {
		return Config{}, fmt.Errorf("configuration must be an object")
	}
	current := &atomic.Pointer[configValues]{}
	current.Store(&configValues{values: values})
	return Config{current: current}, nil
}

// value reads a single scalar from the currently published configuration.
// An empty handle or absent field returns nil. Published maps are immutable,
// so the caller can inspect the scalar after the atomic read completes.
func (c Config) value(key string) any {
	if c.current == nil || c.current.Load() == nil {
		return nil
	}
	return c.current.Load().values[key]
}

// String returns a text setting, or empty text when absent or differently typed.
// It does not trim whitespace or interpret numeric strings. Host normalization
// already checked the field's declared constraints before publication.
func (c Config) String(key string) string {
	value, _ := c.value(key).(string)
	return value
}

// Secret returns a decrypted secret setting using the same rules as String.
// The SDK does not log the value. Plugin authors must keep secrets out of
// HTTP responses, frontend assets, and their own logs.
func (c Config) Secret(key string) string {
	return c.String(key)
}

// Float returns a numeric setting as float64, or zero for an absent or invalid
// number. Monetary RPC requests still use decimal strings; callers should avoid
// using floating arithmetic to construct financial calculations.
func (c Config) Float(key string) float64 {
	value, ok := c.value(key).(json.Number)
	if !ok {
		return 0
	}
	parsed, _ := value.Float64()
	return parsed
}

// Int returns an integer setting, or zero when it cannot fit int64.
// Integral decimal notation is accepted through the numeric representation;
// values outside the signed range remain unavailable to this accessor.
func (c Config) Int(key string) int64 {
	value, ok := c.value(key).(json.Number)
	if !ok {
		return 0
	}
	parsed, err := value.Int64()
	if err == nil {
		return parsed
	}
	n, ok := new(big.Rat).SetString(string(value))
	if !ok || !n.IsInt() || !n.Num().IsInt64() {
		return 0
	}
	return n.Num().Int64()
}

// Bool returns a boolean setting, or false when absent or differently typed.
// String values are not coerced to booleans. Required-field validation remains
// the host's responsibility before passing configuration to the plugin.
func (c Config) Bool(key string) bool {
	value, _ := c.value(key).(bool)
	return value
}

// replace publishes an already decoded snapshot through this live handle.
// Both handles must have been constructed with NewConfig. The new immutable
// map becomes visible in one atomic pointer store to concurrent handlers.
func (c Config) replace(next Config) {
	// Publish the complete immutable object at once so handlers cannot observe a partially updated combination of configuration fields.
	c.current.Store(next.current.Load())
}
