package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/internal/catalogbuild"
)

// catalog.go coordinates registry validation and signed catalog publication.
// The builder owns release acceptance while this CLI owns local state and
// output directories. Publication replaces a complete staged directory so
// index.json, its signature, icons, and ingestion state describe the same run.
// A failed build leaves the previously published directory available.
//
// Submission checks compare authorization against a separate trusted base
// registry, preventing a pull request from granting its author ownership by
// editing the proposed publisher file. New publisher prefixes require an
// explicit maintainer approval flag. GitHub access remains behind the source
// interface so release validation tests use local HTTP fixtures or in-memory
// assets without contacting public repositories or consuming API quotas.

// buildCommand generates a catalog and publishes its matching ingestion state.
// Signing is mandatory unless --unsigned explicitly selects an unpinned custom
// catalog. Failed releases remain diagnostics while valid releases stay publishable.
//
// The complete output directory is replaced after validation, which removes any
// stale signature when an operator explicitly switches to unsigned publication.
// Missing keys never silently downgrade a signed build. Prior state is strictly
// decoded so unsupported fields cannot disappear during an older builder's round trip,
// and ordinary build errors preserve the previously published output generation.
func buildCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("index build", flag.ContinueOnError)
	root := flags.String("registry", ".", "registry repository directory")
	statePath := flags.String("state", "dist/v1/state.json", "previous build state")
	out := flags.String("out", "dist/v1", "catalog output directory")
	name := flags.String("name", "TokenRouter Official", "catalog display name")
	unsigned := flags.Bool("unsigned", false, "publish a custom catalog without a signature")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("index build accepts flags only")
	}
	registry, err := catalogbuild.ReadRegistry(*root)
	if err != nil {
		return err
	}
	previous := catalogbuild.State{}
	data, err := os.ReadFile(*statePath)
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&previous); err != nil {
			return err
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return fmt.Errorf("index build state must contain one JSON value")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var key ed25519.PrivateKey
	if !*unsigned {
		key, err = signingKey("")
		if err != nil {
			return err
		}
	}
	result, err := catalogbuild.Build(context.Background(), registry, &catalogbuild.GitHub{Token: os.Getenv("GITHUB_TOKEN")}, previous, *name, time.Now())
	if err != nil {
		return err
	}
	document, err := json.MarshalIndent(result.Document, "", "  ")
	if err != nil {
		return err
	}
	document = append(document, '\n')
	// Check final encoded bytes before signing or replacing the previous generation.
	if len(document) > index.MaxDocumentBytes {
		return fmt.Errorf("index exceeds %d bytes", index.MaxDocumentBytes)
	}
	var signature []byte
	if !*unsigned {
		signature, err = index.Sign(document, key)
		if err != nil {
			return err
		}
	}
	state, err := json.MarshalIndent(result.State, "", "  ")
	if err != nil {
		return err
	}
	files := map[string][]byte{"index.json": document, "state.json": append(state, '\n')}
	if !*unsigned {
		files["index.json.sig"] = signature
	}
	maps.Copy(files, result.Icons)
	if err := publishDirectory(*out, files); err != nil {
		return err
	}
	for _, failure := range result.Failures {
		fmt.Fprintf(output, "rejected %s\n", failure)
	}
	fmt.Fprintf(output, "published %s\n", filepath.Join(*out, "index.json"))
	return nil
}

// publishDirectory swaps a complete catalog generation into its output location.
// Temporary files are siblings so rename stays on one filesystem. A failed
// final rename restores the previous directory before returning an error.
//
// Callers supply only validated relative filenames and must serialize builds
// for this output path. The previous generation is kept until the new one is
// fully written, then renamed aside during the final swap. This avoids mixed
// signatures and document bytes after ordinary write errors; workflow commits
// publish the resulting directory to remote readers only after successful return.
func publishDirectory(destination string, files map[string][]byte) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".trplugin-index-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	for name, data := range files {
		filename := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filename, data, 0o644); err != nil {
			return err
		}
	}
	backup := stage + "-previous"
	hadPrevious := false
	if _, err := os.Stat(destination); err == nil {
		if err := os.Rename(destination, backup); err != nil {
			return err
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Keep the prior generation until the final rename succeeds; a failed replacement restores that directory instead of leaving mixed catalog files.
	if err := os.Rename(stage, destination); err != nil {
		if hadPrevious {
			if restoreErr := os.Rename(backup, destination); restoreErr != nil {
				return fmt.Errorf("publish failed: %v; restore failed: %w", err, restoreErr)
			}
		}
		return err
	}
	if hadPrevious {
		return os.RemoveAll(backup)
	}
	return nil
}

// submissionCommand checks selected registrations and trusted publisher ownership.
// Existing publishers are authorized against base-registry, never proposed
// ownership changes. New publishers require explicit maintainer approval and
// still need at least one release that passes complete package validation.
func submissionCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("index check-submission", flag.ContinueOnError)
	root := flags.String("registry", ".", "proposed registry directory")
	base := flags.String("base-registry", "", "trusted base registry directory")
	actor := flags.String("actor", "", "pull request author login")
	ids := flags.String("plugins", "", "comma-separated changed plugin IDs")
	allowNew := flags.Bool("allow-new-publisher", false, "maintainer-approved new publisher registration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *actor == "" || *base == "" || *ids == "" || flags.NArg() != 0 {
		return fmt.Errorf("check-submission requires --actor, --base-registry, and --plugins")
	}
	proposed, err := catalogbuild.ReadRegistry(*root)
	if err != nil {
		return err
	}
	trusted, err := catalogbuild.ReadRegistry(*base)
	if err != nil {
		return err
	}
	selected := &catalogbuild.Registry{Publishers: proposed.Publishers}
	source := &catalogbuild.GitHub{Token: os.Getenv("GITHUB_TOKEN")}
	for id := range strings.SplitSeq(*ids, ",") {
		found := false
		for _, registration := range proposed.Plugins {
			if registration.ID != id {
				continue
			}
			found = true
			publisherID, _, _ := strings.Cut(id, ".")
			publisher, exists := trusted.Publishers[publisherID]
			if !exists && !*allowNew {
				return fmt.Errorf("new publisher %s requires maintainer approval", publisherID)
			}
			if exists {
				allowed, err := source.Authorized(context.Background(), *actor, publisher.GitHub)
				if err != nil {
					return err
				}
				if !allowed {
					return fmt.Errorf("author is not authorized for publisher %s", publisherID)
				}
			}
			selected.Plugins = append(selected.Plugins, registration)
		}
		if !found {
			return fmt.Errorf("unknown changed plugin %s", id)
		}
	}
	result, err := catalogbuild.Build(context.Background(), selected, source, catalogbuild.State{}, "Submission validation", time.Now())
	if err != nil {
		return err
	}
	for _, plugin := range result.Document.Plugins {
		if len(plugin.Versions) == 0 {
			return fmt.Errorf("plugin %s has no qualified release: %s", plugin.ID, strings.Join(result.Failures, "; "))
		}
		fmt.Fprintf(output, "valid registration %s\n", plugin.ID)
	}
	return nil
}
