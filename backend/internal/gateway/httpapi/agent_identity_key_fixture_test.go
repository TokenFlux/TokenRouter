package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/stretchr/testify/require"
)

func newTestAgentIdentityKey(t *testing.T) (openai.AgentIdentityKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	return openai.AgentIdentityKey{
		RuntimeID:  "runtime-test",
		PrivateKey: privateKey,
		TaskID:     "task-test",
	}, base64.StdEncoding.EncodeToString(der)
}
