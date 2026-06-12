package qoder

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultAuthDir returns the default Qoder auth directory.
func DefaultAuthDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".qoder", ".auth")
}

// ReadLocalAuth reads and decrypts Qoder authentication data from the local filesystem.
func ReadLocalAuth(authDir string) (*AuthInfo, error) {
	if authDir == "" {
		authDir = DefaultAuthDir()
	}

	machineID, err := os.ReadFile(filepath.Join(authDir, "machine_id"))
	if err != nil {
		return nil, fmt.Errorf("qoder: read machine_id: %w", err)
	}
	machineID = []byte(string(machineID))[:len(machineID)]
	// Trim whitespace/newlines
	for len(machineID) > 0 && (machineID[len(machineID)-1] == '\n' || machineID[len(machineID)-1] == '\r') {
		machineID = machineID[:len(machineID)-1]
	}

	userB64, err := os.ReadFile(filepath.Join(authDir, "user"))
	if err != nil {
		return nil, fmt.Errorf("qoder: read user: %w", err)
	}
	// Trim whitespace
	for len(userB64) > 0 && (userB64[len(userB64)-1] == '\n' || userB64[len(userB64)-1] == '\r') {
		userB64 = userB64[:len(userB64)-1]
	}

	ciphertext, err := base64.StdEncoding.DecodeString(string(userB64))
	if err != nil {
		return nil, fmt.Errorf("qoder: decode user base64: %w", err)
	}

	// Key is first 16 ASCII bytes of machine_id
	key := make([]byte, 16)
	copy(key, machineID)

	plaintext, err := AESDecrypt(ciphertext, key)
	if err != nil {
		return nil, fmt.Errorf("qoder: decrypt user: %w", err)
	}

	var info AuthInfo
	if err := json.Unmarshal(plaintext, &info); err != nil {
		return nil, fmt.Errorf("qoder: parse user JSON: %w", err)
	}
	info.MachineID = string(machineID)

	return &info, nil
}

// LoadLocalIdentity loads identity from local Qoder auth.
func LoadLocalIdentity(authDir string) (*AuthIdentity, *MachineIdentity, error) {
	info, err := ReadLocalAuth(authDir)
	if err != nil {
		return nil, nil, err
	}

	identity := info.ToAuthIdentity()
	machine := &MachineIdentity{
		MachineID:    info.MachineID,
		MachineToken: RandomToken(50),
		MachineType:  RandomHex(18),
	}

	return identity, machine, nil
}
