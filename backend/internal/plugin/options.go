package plugin

import "time"

// options.go defines process-policy inputs independently of the application's
// deployment configuration type. Production defaults provide bounded startup,
// health checks, recovery, and draining, while tests can shorten intervals to
// exercise actual subprocess behavior without waiting through production delays.

// RuntimeOptions controls one host instance's plugin subprocess policy.
type RuntimeOptions struct {
	HostVersion      string        // host release build version
	InstanceID       string        // host process instance identifier
	Timezone         string        // configured IANA timezone name
	HTTPProxy        string        // optional child HTTP proxy URL
	StartTimeout     time.Duration // transport handshake deadline
	InitTimeout      time.Duration // plugin initialization deadline
	HealthInterval   time.Duration // periodic readiness check interval
	HealthTimeout    time.Duration // individual readiness check deadline
	ExitInterval     time.Duration // abnormal exit observation interval
	ResourceInterval time.Duration // CPU and RSS sampling interval
	DrainTimeout     time.Duration // admitted lease draining budget
	ShutdownTimeout  time.Duration // plugin shutdown callback budget
	CrashWindow      time.Duration // rolling abnormal exit window
	MaxCrashes       int           // crash-window intervention threshold
	MaxLeases        int           // per-plugin admitted call limit
	// Backoffs is copied by the runtime; the final duration repeats after the sequence is exhausted until the crash threshold intervenes.
	Backoffs []time.Duration
	// Now supplies observation timestamps and crash-window comparisons; nil selects the system clock at construction.
	Now func() time.Time
}

// WithDefaults fills omitted process-policy values without changing explicit inputs.
// Nonpositive durations and counts select production defaults. Backoff slices
// are copied so test or application configuration cannot race with a running loop.
func (o RuntimeOptions) WithDefaults() RuntimeOptions {
	if o.StartTimeout <= 0 {
		o.StartTimeout = 10 * time.Second
	}
	if o.InitTimeout <= 0 {
		o.InitTimeout = 15 * time.Second
	}
	if o.HealthInterval <= 0 {
		o.HealthInterval = 15 * time.Second
	}
	if o.HealthTimeout <= 0 {
		o.HealthTimeout = 3 * time.Second
	}
	if o.ExitInterval <= 0 {
		o.ExitInterval = time.Second
	}
	if o.ResourceInterval <= 0 {
		o.ResourceInterval = 30 * time.Second
	}
	if o.DrainTimeout <= 0 {
		o.DrainTimeout = 10 * time.Second
	}
	if o.ShutdownTimeout <= 0 {
		o.ShutdownTimeout = 5 * time.Second
	}
	if o.CrashWindow <= 0 {
		o.CrashWindow = 10 * time.Minute
	}
	if o.MaxCrashes <= 0 {
		o.MaxCrashes = 5
	}
	if o.MaxLeases <= 0 {
		o.MaxLeases = 32
	}
	if len(o.Backoffs) == 0 {
		o.Backoffs = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second}
	} else {
		o.Backoffs = append([]time.Duration(nil), o.Backoffs...)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timezone == "" {
		o.Timezone = "UTC"
	}
	return o
}
