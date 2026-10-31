package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
)

// signing.go creates publisher signing keys and signs validated catalog bytes.
// Private keys are written only to an explicitly named new file with owner-only
// permissions. Command output contains the public key or signature path, keeping
// secret key material out of normal terminal logs and workflow summaries.

// keygenCommand creates a new Ed25519 key file and prints its public key.
// Existing files are never overwritten. The private file contains base64
// suitable for the index workflow's INDEX_SIGNING_KEY secret.
func keygenCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	keyPath := flags.String("out", "", "new private-key file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || flags.NArg() != 0 {
		return fmt.Errorf("usage: trplugin keygen --out <new-private-key-file>")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(*keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, base64.StdEncoding.EncodeToString(private)+"\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Fprintln(output, base64.StdEncoding.EncodeToString(public))
	return nil
}

// indexCommand selects catalog signing or registry publication subcommands.
func indexCommand(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: trplugin index sign|build|check-submission")
	}
	switch args[0] {
	case "sign":
		return signCommand(args[1:], output)
	case "build":
		return buildCommand(args[1:], output)
	case "check-submission":
		return submissionCommand(args[1:], output)
	default:
		return fmt.Errorf("unknown index command %q", args[0])
	}
}

// signCommand validates a catalog then signs its exact on-disk bytes.
// The private key comes from --key or INDEX_SIGNING_KEY, and is never printed.
// Successful output identifies the separate base64 signature file.
func signCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("index sign", flag.ContinueOnError)
	input := flags.String("in", "index.json", "catalog document")
	out := flags.String("out", "", "signature output path")
	keyPath := flags.String("key", "", "private-key file, otherwise INDEX_SIGNING_KEY")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("index sign accepts flags only")
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	if _, err := index.Parse(data); err != nil {
		return err
	}
	key, err := signingKey(*keyPath)
	if err != nil {
		return err
	}
	signature, err := index.Sign(data, key)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = *input + ".sig"
	}
	if err := os.WriteFile(*out, signature, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(output, *out)
	return nil
}

// signingKey decodes an explicitly supplied file or the workflow environment secret.
// Empty and malformed keys fail with a generic diagnostic that does not
// disclose the input. Only complete Ed25519 private keys are accepted.
func signingKey(path string) (ed25519.PrivateKey, error) {
	value := os.Getenv("INDEX_SIGNING_KEY")
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		value = string(data)
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(value))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("a valid base64 Ed25519 private key is required")
	}
	return ed25519.PrivateKey(key), nil
}
