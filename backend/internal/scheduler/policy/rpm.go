package policy

// RPMAllowance 保留旧三区模型的数值标识；调用方将其投影为原展示类型。
type RPMAllowance int

const (
	RPMAllowed RPMAllowance = iota
	RPMStickyOnly
	RPMBlocked
)

// CheckRPM 保留严格小于边界和 sticky_exempt 不设红区的语义。
func CheckRPM(current, base, buffer int, strategy string) RPMAllowance {
	if base <= 0 || current < base {
		return RPMAllowed
	}
	if strategy == "sticky_exempt" {
		return RPMStickyOnly
	}
	if current < base+buffer {
		return RPMStickyOnly
	}
	return RPMBlocked
}

// RPMStickyBuffer 保留并发加会话容量、显式 override 与 base/5 下限。
func RPMStickyBuffer(base, concurrency, sessions, override int) int {
	if override > 0 {
		return override
	}
	if base <= 0 {
		return 0
	}
	if concurrency < 0 {
		concurrency = 0
	}
	if sessions < 0 {
		sessions = 0
	}
	buffer := concurrency + sessions
	floor := base / 5
	if floor < 1 {
		floor = 1
	}
	if buffer < floor {
		buffer = floor
	}
	return buffer
}
