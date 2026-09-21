//go:build integration

package repository

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity/provider"
	identityredis "github.com/TokenFlux/TokenRouter/internal/identity/rediscache"
	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"
)

// TestS05PasskeySDKCeremony 使用真实 SDK、签名、PostgreSQL 与 Redis，验证消费先于解析及凭据更新。
// 本地软件认证器只验证服务端协议行为，不声称覆盖真实硬件或浏览器交互。
func TestS05PasskeySDKCeremony(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &identity.User{})
	users := identitypostgres.NewUserStore(client, integrationDB)
	repo := identitypostgres.NewPasskeyRepository(integrationDB)
	verifier, e := provider.NewPasskeyVerifier(provider.PasskeyOptions{Enabled: true, RPID: "example.com", RPDisplayName: "S05", RPOrigins: []string{"https://example.com"}})
	require.NoError(t, e)
	core := identity.NewPasskeyService(true, verifier, repo, identityredis.NewPasskeySessionStore(testRedis(t)), users)
	encode := base64.RawURLEncoding.EncodeToString
	marshal := func(v any) []byte { b, e := json.Marshal(v); require.NoError(t, e); return b }
	_, broken, e := core.BeginRegistration(ctx, user.ID)
	require.NoError(t, e)
	_, e = core.FinishRegistration(ctx, user.ID, broken, "broken", bytes.NewBufferString("{"))
	require.ErrorIs(t, e, identity.ErrPasskeyVerify)
	_, e = core.FinishRegistration(ctx, user.ID, broken, "replay", bytes.NewBufferString("{"))
	require.ErrorIs(t, e, identity.ErrPasskeySession)
	creation, token, e := core.BeginRegistration(ctx, user.ID)
	require.NoError(t, e)
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, e)
	public, e := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: private.X.FillBytes(make([]byte, 32)), -3: private.Y.FillBytes(make([]byte, 32))})
	require.NoError(t, e)
	credentialID := []byte("s05-local-software-authenticator")
	rpHash := sha256.Sum256([]byte("example.com"))
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, 0x45, 0, 0, 0, 0)
	auth = append(auth, make([]byte, 16)...)
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(credentialID)))
	auth = append(auth, credentialID...)
	auth = append(auth, public...)
	attestation, e := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	require.NoError(t, e)
	clientData := marshal(map[string]any{"type": "webauthn.create", "challenge": creation.Response.Challenge, "origin": "https://example.com", "crossOrigin": false})
	registration := marshal(map[string]any{"id": encode(credentialID), "rawId": encode(credentialID), "type": "public-key", "response": map[string]any{"clientDataJSON": encode(clientData), "attestationObject": encode(attestation), "transports": []string{"internal"}}, "clientExtensionResults": map[string]any{"credProps": map[string]any{"rk": true}}})
	created, e := core.FinishRegistration(ctx, user.ID, token, "Local S05", bytes.NewReader(registration))
	require.NoError(t, e)
	require.NotZero(t, created.ID)
	record, e := repo.GetByCredentialID(ctx, credentialID)
	require.NoError(t, e)
	require.Equal(t, uint32(0), record.Credential.Authenticator.SignCount)
	assert := func(origin string, valid bool) (string, []byte) {
		request, token, e := core.BeginLogin(ctx)
		require.NoError(t, e)
		data := marshal(map[string]any{"type": "webauthn.get", "challenge": request.Response.Challenge, "origin": origin, "crossOrigin": false})
		auth := append([]byte{}, rpHash[:]...)
		auth = append(auth, 0x05)
		auth = binary.BigEndian.AppendUint32(auth, 1)
		dataHash := sha256.Sum256(data)
		signed := append(append([]byte{}, auth...), dataHash[:]...)
		digest := sha256.Sum256(signed)
		sig, e := ecdsa.SignASN1(rand.Reader, private, digest[:])
		require.NoError(t, e)
		if !valid {
			sig[len(sig)-1] ^= 1
		}
		return token, marshal(map[string]any{"id": encode(credentialID), "rawId": encode(credentialID), "type": "public-key", "response": map[string]any{"clientDataJSON": encode(data), "authenticatorData": encode(auth), "signature": encode(sig), "userHandle": encode(record.UserHandle)}})
	}
	for _, sample := range []struct {
		name, origin string
		valid        bool
	}{{"origin", "https://wrong.example", true}, {"signature", "https://example.com", false}} {
		t.Run(sample.name, func(t *testing.T) {
			token, body := assert(sample.origin, sample.valid)
			_, e := core.FinishLogin(ctx, token, bytes.NewReader(body))
			require.ErrorIs(t, e, identity.ErrPasskeyVerify)
			_, e = core.FinishLogin(ctx, token, bytes.NewReader(body))
			require.ErrorIs(t, e, identity.ErrPasskeySession)
		})
	}
	login, body := assert("https://example.com", true)
	got, e := core.FinishLogin(ctx, login, bytes.NewReader(body))
	require.NoError(t, e)
	require.Equal(t, user.ID, got.ID)
	record, e = repo.GetByCredentialID(ctx, credentialID)
	require.NoError(t, e)
	require.Equal(t, uint32(1), record.Credential.Authenticator.SignCount)
	require.NotNil(t, record.LastUsedAt)
	_, e = core.FinishLogin(ctx, login, bytes.NewReader(body))
	require.ErrorIs(t, e, identity.ErrPasskeySession)
}
