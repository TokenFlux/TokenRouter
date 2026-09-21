package identity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsReservedEmail_DingTalkDomain(t *testing.T) {
	require.True(t, IsReservedEmail("dingtalk-123@dingtalk-connect.invalid"))
	require.True(t, IsReservedEmail("DINGTALK-456@DINGTALK-CONNECT.INVALID")) // case-insensitive
	require.False(t, IsReservedEmail("real@dingtalk.com"))
}
