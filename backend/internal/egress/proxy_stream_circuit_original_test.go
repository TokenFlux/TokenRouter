package egress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIProxyStreamCircuitThresholdTTLAndSuccessReset(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := NewProxyStreamCircuit(ProxyStreamCircuitSettings{
		FailureThreshold: 2,
		FailureWindow:    time.Minute,
		QuarantineTTL:    10 * time.Minute,
		MaxEntries:       16,
	})

	tripped, _ := circuit.RecordFailure(1, base)
	require.False(t, tripped)
	require.False(t, circuit.IsBlocked(1, base))
	require.True(t, circuit.RecordSuccess(1))

	tripped, _ = circuit.RecordFailure(1, base.Add(10*time.Second))
	require.False(t, tripped, "success must clear the previous failure observation")
	tripped, until := circuit.RecordFailure(1, base.Add(20*time.Second))
	require.True(t, tripped)
	require.Equal(t, base.Add(20*time.Second+10*time.Minute), until)
	require.True(t, circuit.IsBlocked(1, until.Add(-time.Nanosecond)))
	require.False(t, circuit.IsBlocked(1, until), "TTL expiry must re-admit the proxy")

	tripped, _ = circuit.RecordFailure(2, base)
	require.False(t, tripped)
	tripped, _ = circuit.RecordFailure(2, base.Add(2*time.Minute))
	require.False(t, tripped, "failures outside the window must not accumulate")
}

// 同一复用连接引发的并发断流只应累计一次，独立的后续故障仍可触发隔离。
func TestOpenAIProxyStreamCircuitCollapsesBurstFailures(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := NewProxyStreamCircuit(ProxyStreamCircuitSettings{
		FailureThreshold: 2,
		FailureWindow:    time.Minute,
		QuarantineTTL:    10 * time.Minute,
		CollapseInterval: 3 * time.Second,
		MaxEntries:       16,
	})

	tripped, _ := circuit.RecordFailure(1, base)
	require.False(t, tripped)
	tripped, _ = circuit.RecordFailure(1, base.Add(time.Second))
	require.False(t, tripped, "折叠窗口内的并发断流不能重复累计")
	tripped, _ = circuit.RecordFailure(1, base.Add(2*time.Second))
	require.False(t, tripped, "折叠窗口内的并发断流不能重复累计")
	require.False(t, circuit.IsBlocked(1, base.Add(2*time.Second)))

	tripped, _ = circuit.RecordFailure(1, base.Add(5*time.Second))
	require.True(t, tripped, "折叠窗口后的独立故障应触发隔离")
	require.True(t, circuit.IsBlocked(1, base.Add(5*time.Second)))
}

// 显式禁用后不记录故障，也不产生任何隔离容量。
func TestOpenAIProxyStreamCircuitDisabled(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := NewProxyStreamCircuit(ProxyStreamCircuitSettings{
		Disabled:         true,
		FailureThreshold: 1,
		FailureWindow:    time.Minute,
		QuarantineTTL:    10 * time.Minute,
		MaxEntries:       16,
	})

	tripped, _ := circuit.RecordFailure(1, base)
	require.False(t, tripped)
	require.False(t, circuit.IsBlocked(1, base))
	require.Equal(t, 0, circuit.ActiveBlockCount(base))
}

func TestOpenAIProxyStreamCircuitActiveBlockCount(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := NewProxyStreamCircuit(ProxyStreamCircuitSettings{
		FailureThreshold: 1,
		FailureWindow:    time.Minute,
		QuarantineTTL:    10 * time.Minute,
		MaxEntries:       16,
	})

	require.Equal(t, 0, circuit.ActiveBlockCount(base))
	tripped, until := circuit.RecordFailure(1, base)
	require.True(t, tripped)
	circuit.RecordFailure(2, base)
	require.Equal(t, 2, circuit.ActiveBlockCount(base.Add(time.Second)))
	require.Equal(t, 0, circuit.ActiveBlockCount(until), "到期隔离不应计入活跃数量")
}

func TestOpenAIProxyStreamCircuitBoundsEntries(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := NewProxyStreamCircuit(ProxyStreamCircuitSettings{
		FailureThreshold: 1,
		FailureWindow:    time.Minute,
		QuarantineTTL:    10 * time.Minute,
		MaxEntries:       2,
	})

	circuit.RecordFailure(1, base)
	circuit.RecordFailure(2, base.Add(time.Second))
	circuit.RecordFailure(3, base.Add(2*time.Second))

	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	require.Len(t, circuit.entries, 2)
	_, oldestRetained := circuit.entries[1]
	require.False(t, oldestRetained, "the oldest entry must be evicted at the bound")
}
