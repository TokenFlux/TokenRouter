package session

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

// ownerReaderProbe 只提供持久化归属事实，验证授权条件与读取顺序。
type ownerReaderProbe struct {
	user, key int64
	found     bool
	err       error
	reads     int
}

func (p *ownerReaderProbe) GetHTTPResponseOwner(context.Context, int64, string) (int64, int64, bool, error) {
	p.reads++
	return p.user, p.key, p.found, p.err
}
func TestHTTPResponseOwnershipPreservesTenantAndLegacyKeys(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		user, key, requestUser, requestKey int64
		found, want                        bool
	}{
		{"same-user-another-key", 1, 4, 1, 5, true, true},
		{"other-user-same-key", 1, 4, 2, 4, true, false},
		{"legacy-key-only", 0, 4, 2, 4, true, true},
		{"legacy-other-key", 0, 4, 2, 5, true, false},
		{"missing", 1, 4, 1, 4, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &ownerReaderProbe{user: tc.user, key: tc.key, found: tc.found}
			ok, err := ValidateHTTPResponseOwner(context.Background(), func() HTTPResponseOwnerReader { return reader }, 7, "resp_1", tc.requestUser, tc.requestKey)
			require.NoError(t, err)
			require.Equal(t, tc.want, ok)
			require.Equal(t, 1, reader.reads)
		})
	}
}
func TestHTTPResponseOwnershipPreservesShortCircuitAndFailure(t *testing.T) {
	reads := 0
	storeErr := errors.New("owner read unavailable")
	reader := &ownerReaderProbe{found: true, user: 1, key: 2, err: storeErr}
	get := func() HTTPResponseOwnerReader { reads++; return reader }
	for _, tc := range []struct {
		id        string
		user, key int64
	}{{" ", 1, 2}, {"resp_1", 0, 2}, {"resp_1", 1, 0}} {
		ok, err := ValidateHTTPResponseOwner(context.Background(), get, 1, tc.id, tc.user, tc.key)
		require.NoError(t, err)
		require.False(t, ok)
	}
	require.Zero(t, reads)
	ok, err := ValidateHTTPResponseOwner(context.Background(), get, 1, "resp_1", 1, 2)
	require.ErrorIs(t, err, storeErr)
	require.False(t, ok)
	require.Equal(t, 1, reads)
}
