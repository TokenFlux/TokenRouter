package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

const (
	qoderModelAliasesFileName = "qoder-model-aliases.json"
	qoderModelSyncTimeout     = 30 * time.Second
)

type qoderModelSyncRunner func(ctx context.Context, source string) ([]byte, error)

type QoderModelSyncService struct {
	scriptPath  string
	persistPath string
	runner      qoderModelSyncRunner
}

type QoderModelSyncInput struct {
	Source string
	Apply  bool
}

type QoderModelSyncResult struct {
	Source        string                  `json:"source"`
	Applied       bool                    `json:"applied"`
	ScriptPath    string                  `json:"script_path"`
	PersistPath   string                  `json:"persist_path"`
	IncomingCount int                     `json:"incoming_count"`
	CurrentCount  int                     `json:"current_count"`
	FinalCount    int                     `json:"final_count"`
	Added         []QoderModelAliasRecord `json:"added"`
	Removed       []QoderModelAliasRecord `json:"removed"`
	Changed       []QoderModelAliasChange `json:"changed"`
	Preserved     []QoderModelAliasRecord `json:"preserved"`
	Models        []QoderModelAliasRecord `json:"models"`
}

type QoderModelAliasRecord struct {
	Alias       string `json:"alias"`
	Key         string `json:"key"`
	Source      string `json:"source"`
	Provider    string `json:"provider,omitempty"`
	Notes       string `json:"notes,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type QoderModelAliasChange struct {
	Alias  string                `json:"alias"`
	Before QoderModelAliasRecord `json:"before"`
	After  QoderModelAliasRecord `json:"after"`
	Fields []string              `json:"fields"`
}

type qoderModelSyncScriptEntry struct {
	Alias       string `json:"alias"`
	Key         string `json:"key"`
	Provider    string `json:"provider"`
	Notes       string `json:"notes"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type qoderModelAliasesPersisted struct {
	Version int                     `json:"version"`
	Models  []QoderModelAliasRecord `json:"models"`
}

func NewQoderModelSyncService(cfg *config.Config) *QoderModelSyncService {
	dataDir := "./data"
	if cfg != nil && strings.TrimSpace(cfg.Pricing.DataDir) != "" {
		dataDir = strings.TrimSpace(cfg.Pricing.DataDir)
	}
	svc := &QoderModelSyncService{
		scriptPath:  strings.TrimSpace(cfg.Qoder.ModelSyncScriptPath),
		persistPath: filepath.Join(dataDir, qoderModelAliasesFileName),
	}
	svc.runner = svc.runSyncScript
	_ = svc.loadPersisted()
	return svc
}

func (s *QoderModelSyncService) SyncModels(ctx context.Context, input QoderModelSyncInput) (*QoderModelSyncResult, error) {
	if s == nil {
		return nil, errors.New("qoder model sync service is not configured")
	}
	source := strings.ToLower(strings.TrimSpace(input.Source))
	if source == "" {
		source = "local"
	}
	if source != "local" && source != "cli" {
		return nil, fmt.Errorf("unsupported qoder model sync source: %s", input.Source)
	}
	runner := s.runner
	if runner == nil {
		runner = s.runSyncScript
	}
	out, err := runner(ctx, source)
	if err != nil {
		return nil, err
	}
	incoming, err := parseQoderModelSyncOutput(out)
	if err != nil {
		return nil, err
	}

	current := currentQoderModelAliases()
	baseline := cloneQoderModelAliases(defaultQoderModelAliases)
	finalAliases, preserved := buildQoderModelSyncAliases(current, incoming)
	added, removed, changed := diffQoderModelAliases(baseline, finalAliases, incoming)
	result := &QoderModelSyncResult{
		Source:        source,
		Applied:       input.Apply,
		ScriptPath:    s.scriptPath,
		PersistPath:   s.persistPath,
		IncomingCount: len(incoming),
		CurrentCount:  len(current),
		FinalCount:    len(finalAliases),
		Added:         added,
		Removed:       removed,
		Changed:       changed,
		Preserved:     preserved,
		Models:        qoderAliasRecordsFromMap(finalAliases),
	}
	if !input.Apply {
		return result, nil
	}
	if err := s.persistAliases(finalAliases); err != nil {
		return nil, err
	}
	applyQoderModelAliases(finalAliases)
	return result, nil
}

func (s *QoderModelSyncService) runSyncScript(ctx context.Context, source string) ([]byte, error) {
	scriptPath := strings.TrimSpace(s.scriptPath)
	if scriptPath == "" {
		return nil, errors.New("qoder model sync is disabled: configure qoder.model_sync_script_path")
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, qoderModelSyncTimeout)
	defer cancel()
	args := []string{scriptPath}
	if source == "cli" {
		args = append(args, "--cli")
	}
	args = append(args, "--json")
	cmd := exec.CommandContext(timeoutCtx, "python3", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if timeoutCtx.Err() != nil {
		return nil, fmt.Errorf("sync qoder models timed out: %w", timeoutCtx.Err())
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("sync qoder models: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("sync qoder models: %w", err)
	}
	return out, nil
}

func parseQoderModelSyncOutput(out []byte) (map[string]qoderModelInfo, error) {
	var entries []qoderModelSyncScriptEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parse qoder model sync output: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("qoder model sync returned no models")
	}
	models := make(map[string]qoderModelInfo, len(entries))
	for _, entry := range entries {
		key := strings.TrimSpace(entry.Key)
		if key == "" {
			return nil, errors.New("qoder model sync returned a model without key")
		}
		alias := strings.TrimSpace(entry.Alias)
		if alias == "" {
			alias = key
		}
		models[alias] = qoderModelInfo{
			Key:         key,
			Source:      "system",
			Provider:    strings.TrimSpace(entry.Provider),
			Notes:       strings.TrimSpace(entry.Notes),
			DisplayName: strings.TrimSpace(entry.DisplayName),
			Description: strings.TrimSpace(entry.Description),
		}
	}
	return models, nil
}

func normalizeQoderModelInfo(info qoderModelInfo, fallback qoderModelInfo) qoderModelInfo {
	if strings.TrimSpace(info.Key) == "" {
		info.Key = fallback.Key
	}
	if strings.TrimSpace(info.Source) == "" {
		info.Source = fallback.Source
	}
	if strings.TrimSpace(info.Provider) == "" {
		info.Provider = fallback.Provider
	}
	if strings.TrimSpace(info.Notes) == "" {
		info.Notes = fallback.Notes
	}
	if strings.TrimSpace(info.DisplayName) == "" {
		info.DisplayName = fallback.DisplayName
	}
	if strings.TrimSpace(info.Description) == "" {
		info.Description = fallback.Description
	}
	return info
}

func qoderModelInfoForAlias(models map[string]qoderModelInfo, alias string, fallback qoderModelInfo) qoderModelInfo {
	if info, ok := models[alias]; ok {
		return normalizeQoderModelInfo(info, fallback)
	}
	if key := strings.TrimSpace(fallback.Key); key != "" {
		if info, ok := models[key]; ok {
			return normalizeQoderModelInfo(info, fallback)
		}
	}
	return fallback
}

func buildQoderPublicModelAliases(models map[string]qoderModelInfo) map[string]qoderModelInfo {
	aliases := make(map[string]qoderModelInfo, len(defaultQoderModelAliases))
	for alias, fallback := range defaultQoderModelAliases {
		aliases[alias] = qoderModelInfoForAlias(models, alias, fallback)
	}
	return aliases
}

func buildQoderModelSyncAliases(current map[string]qoderModelInfo, incoming map[string]qoderModelInfo) (map[string]qoderModelInfo, []QoderModelAliasRecord) {
	finalAliases := buildQoderPublicModelAliases(incoming)
	return finalAliases, nil
}

func qoderAliasWasInSource(source map[string]qoderModelInfo, alias string, info qoderModelInfo) bool {
	if _, ok := source[alias]; ok {
		return true
	}
	key := strings.TrimSpace(info.Key)
	if key == "" {
		return false
	}
	_, ok := source[key]
	return ok
}

func diffQoderModelAliases(current map[string]qoderModelInfo, finalAliases map[string]qoderModelInfo, incoming map[string]qoderModelInfo) ([]QoderModelAliasRecord, []QoderModelAliasRecord, []QoderModelAliasChange) {
	var added []QoderModelAliasRecord
	var removed []QoderModelAliasRecord
	var changed []QoderModelAliasChange

	for alias, next := range finalAliases {
		if !qoderAliasWasInSource(incoming, alias, next) {
			continue
		}
		prev, exists := current[alias]
		if !exists {
			added = append(added, qoderAliasRecord(alias, next))
			continue
		}
		if fields := changedQoderModelAliasFields(prev, next); len(fields) > 0 {
			changed = append(changed, QoderModelAliasChange{
				Alias:  alias,
				Before: qoderAliasRecord(alias, prev),
				After:  qoderAliasRecord(alias, next),
				Fields: fields,
			})
		}
	}
	for alias, prev := range current {
		if alias != prev.Key {
			continue
		}
		if _, ok := finalAliases[alias]; !ok {
			removed = append(removed, qoderAliasRecord(alias, prev))
		}
	}

	sortQoderModelAliasRecords(added)
	sortQoderModelAliasRecords(removed)
	sort.Slice(changed, func(i, j int) bool {
		return changed[i].Alias < changed[j].Alias
	})
	return added, removed, changed
}

func changedQoderModelAliasFields(before, after qoderModelInfo) []string {
	var fields []string
	if before.Key != after.Key {
		fields = append(fields, "key")
	}
	if before.Source != after.Source {
		fields = append(fields, "source")
	}
	if before.Provider != after.Provider {
		fields = append(fields, "provider")
	}
	if before.Notes != after.Notes {
		fields = append(fields, "notes")
	}
	if before.DisplayName != after.DisplayName {
		fields = append(fields, "display_name")
	}
	if before.Description != after.Description {
		fields = append(fields, "description")
	}
	return fields
}

func (s *QoderModelSyncService) loadPersisted() error {
	if s == nil || strings.TrimSpace(s.persistPath) == "" {
		return nil
	}
	body, err := os.ReadFile(s.persistPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var persisted qoderModelAliasesPersisted
	if err := json.Unmarshal(body, &persisted); err != nil {
		return err
	}
	aliases := map[string]qoderModelInfo{}
	for _, model := range persisted.Models {
		alias := strings.TrimSpace(model.Alias)
		key := strings.TrimSpace(model.Key)
		if alias == "" || key == "" {
			continue
		}
		source := strings.TrimSpace(model.Source)
		if source == "" {
			source = "system"
		}
		aliases[alias] = qoderModelInfo{
			Key:         key,
			Source:      source,
			Provider:    strings.TrimSpace(model.Provider),
			Notes:       strings.TrimSpace(model.Notes),
			DisplayName: strings.TrimSpace(model.DisplayName),
			Description: strings.TrimSpace(model.Description),
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	applyQoderModelAliases(buildQoderPublicModelAliases(aliases))
	return nil
}

func (s *QoderModelSyncService) persistAliases(aliases map[string]qoderModelInfo) error {
	if s == nil || strings.TrimSpace(s.persistPath) == "" {
		return errors.New("qoder model alias persist path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.persistPath), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(qoderModelAliasesPersisted{
		Version: 1,
		Models:  qoderAliasRecordsFromMap(aliases),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.persistPath, append(body, '\n'), 0o644)
}

func qoderAliasRecordsFromMap(aliases map[string]qoderModelInfo) []QoderModelAliasRecord {
	records := make([]QoderModelAliasRecord, 0, len(aliases))
	for alias, info := range aliases {
		records = append(records, qoderAliasRecord(alias, info))
	}
	sortQoderModelAliasRecords(records)
	return records
}

func qoderAliasRecord(alias string, info qoderModelInfo) QoderModelAliasRecord {
	return QoderModelAliasRecord{
		Alias:       alias,
		Key:         info.Key,
		Source:      info.Source,
		Provider:    info.Provider,
		Notes:       info.Notes,
		DisplayName: info.DisplayName,
		Description: info.Description,
	}
}

func sortQoderModelAliasRecords(records []QoderModelAliasRecord) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].Alias < records[j].Alias
	})
}
