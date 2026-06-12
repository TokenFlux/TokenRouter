package qoder

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CenterBaseURL is the default center service URL.
const CenterBaseURL = "https://center.qoder.sh"

// APIBaseURL is the default Qoder API base URL.
const APIBaseURL = "https://api1.qoder.sh"

// ClientVersion is the COSY protocol version.
const ClientVersion = "0.1.43"

// GenerateRequestID generates a random request ID.
func GenerateRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// RandomToken generates a random URL-safe token of the given length.
func RandomToken(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return base64URLEncode(b)
}

// RandomHex generates a random hex string of the given length.
func RandomHex(length int) string {
	b := make([]byte, (length+1)/2)
	rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

func base64URLEncode(b []byte) string {
	// Manual URL-safe base64 encoding without padding
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var result strings.Builder
	result.Grow((len(b)*8 + 5) / 6)

	for i := 0; i < len(b); i += 3 {
		var val uint32
		remaining := len(b) - i

		val |= uint32(b[i]) << 16
		if remaining > 1 {
			val |= uint32(b[i+1]) << 8
		}
		if remaining > 2 {
			val |= uint32(b[i+2])
		}

		result.WriteByte(alphabet[(val>>18)&0x3f])
		result.WriteByte(alphabet[(val>>12)&0x3f])
		if remaining > 1 {
			result.WriteByte(alphabet[(val>>6)&0x3f])
		}
		if remaining > 2 {
			result.WriteByte(alphabet[val&0x3f])
		}
	}
	return result.String()
}

// NewMachine creates a new random machine identity.
func NewMachine() *MachineIdentity {
	return &MachineIdentity{
		MachineID:    RandomHex(36), // UUID format length
		MachineToken: RandomToken(50),
		MachineType:  RandomHex(18),
	}
}

// ExchangePAT exchanges a Personal Access Token for an AuthIdentity.
func ExchangePAT(pat string, machine *MachineIdentity, centerURL string) (*AuthIdentity, error) {
	if centerURL == "" {
		centerURL = CenterBaseURL
	}

	inner := map[string]interface{}{
		"personalToken":      pat,
		"securityOauthToken": "",
		"refreshToken":       "",
		"needRefresh":        false,
		"authInfo":           map[string]interface{}{},
	}
	innerJSON, _ := json.Marshal(inner)

	outer := map[string]interface{}{
		"payload":       string(innerJSON),
		"encodeVersion": "1",
	}
	outerJSON, _ := json.Marshal(outer)

	body := EncodeJSON(outerJSON)

	req, err := http.NewRequest("POST",
		centerURL+"/algo/api/v3/user/jobToken?Encode=1",
		strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("qoder: PAT exchange request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Go-http-client/2.0")
	req.Header.Set("cosy-machinetoken", machine.MachineToken)
	req.Header.Set("cosy-machinetype", machine.MachineType)
	req.Header.Set("cosy-machineid", machine.MachineID)
	req.Header.Set("cosy-version", ClientVersion)
	req.Header.Set("cosy-clienttype", "5")
	req.Header.Set("login-version", "v2")
	req.Header.Set("appcode", AppCode)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: PAT exchange request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("qoder: PAT exchange failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("qoder: parse PAT exchange response: %w", err)
	}

	token, _ := data["securityOauthToken"].(string)
	refreshToken, _ := data["refreshToken"].(string)
	name, _ := data["name"].(string)
	uid, _ := data["id"].(string)
	userType, _ := data["userType"].(string)
	if userType == "" {
		userType = "personal_standard"
	}

	return &AuthIdentity{
		Name:               name,
		AID:                uid,
		UID:                uid,
		UserType:           userType,
		SecurityOauthToken:  token,
		RefreshToken:       refreshToken,
	}, nil
}

// pathWithoutAlgo removes the "/algo" prefix from a URL path for signature computation.
func pathWithoutAlgo(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return strings.TrimPrefix(u.Path, "/algo")
}
