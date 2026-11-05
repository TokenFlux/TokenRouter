package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// upgrade_nodes.go moves named YAML settings while retaining their comments.
// Migration uses dotted configuration paths independently of application struct
// layout. Existing key nodes travel with their values, including head, line,
// and foot comments, while newly introduced parent mappings are appended.
//
// Moving leaves can leave an empty source subtree; pruning removes that subtree
// only after all moves finish. Destination conflicts fail before the original
// file is written. YAML aliases are expanded before transformations because
// mutating an aliased mapping through one configuration path would otherwise
// change settings at another path or leave an anchor pointing at removed data.
// YAML merge membership uses literal key names; dotted path interpretation only
// begins when the version 1 canonicalizer reorganizes recognized settings.

// configPath returns the value at a dotted mapping path, or nil when absent.
// Non-mapping parents also return nil, allowing migration callers to distinguish
// unreachable fields from supplied scalar values without creating intermediate nodes.
func configPath(root *yaml.Node, path string) *yaml.Node {
	current := root
	for part := range strings.SplitSeq(path, ".") {
		if current == nil || current.Kind != yaml.MappingNode {
			return nil
		}
		current = configMappingValue(current, part)
	}
	return current
}

// configMappingValue returns the value for one literal key in a mapping.
// The caller supplies a nonnil mapping node; missing keys return nil, and dots
// in names have no path meaning at this lookup boundary.
func configMappingValue(root *yaml.Node, name string) *yaml.Node {
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == name {
			return root.Content[i+1]
		}
	}
	return nil
}

// configValueNode encodes one typed migration value as an independent YAML node.
// Callers supply scalar primitives or string lists, whose encoding cannot fail;
// unsupported values panic to expose a programming error in a migration table.
func configValueNode(value any) *yaml.Node {
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		panic(err)
	}
	return &node
}

// setConfigPath installs a value, preserving comments on an existing key.
// Null parents become mappings when an eligible field is supplied. Other
// non-mapping parents are rejected so malformed input cannot erase neighboring settings.
func setConfigPath(root *yaml.Node, path string, value *yaml.Node) error {
	return putConfigPath(root, path, nil, value)
}

// putConfigPath installs a value and optionally relocates an existing key node.
// Missing or null parents become mappings, while other non-mapping parents fail
// instead of discarding neighboring settings. Replacing a leaf retains its key
// comments and transfers value comments when the replacement has none. A supplied
// key node keeps its comments while taking the destination's final name.
//
// This routine changes the in-memory document only; migration callers discard
// a partially changed document when any subsequent transformation reports an error.
func putConfigPath(root *yaml.Node, path string, key, value *yaml.Node) error {
	parts := strings.Split(path, ".")
	current := root
	for _, part := range parts[:len(parts)-1] {
		if current.Kind != yaml.MappingNode {
			return fmt.Errorf("configuration %s has a non-mapping parent", path)
		}
		next := configPath(current, part)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			current.Content = append(current.Content, configValueNode(part), next)
		} else if next.Tag == "!!null" {
			// An eligible alias or explicit environment binding can supply a child of
			// an unset section; preserve the section's comments while creating its mapping.
			next.Kind = yaml.MappingNode
			next.Tag = "!!map"
			next.Value = ""
		}
		current = next
	}
	if current.Kind != yaml.MappingNode {
		return fmt.Errorf("configuration %s has a non-mapping parent", path)
	}
	name := parts[len(parts)-1]
	for i := 0; i < len(current.Content); i += 2 {
		if current.Content[i].Value == name {
			previous := current.Content[i+1]
			// Value transformations replace scalar nodes, so retain their inline
			// and surrounding comments before installing the new representation.
			if value.HeadComment == "" {
				value.HeadComment = previous.HeadComment
			}
			if value.LineComment == "" {
				value.LineComment = previous.LineComment
			}
			if value.FootComment == "" {
				value.FootComment = previous.FootComment
			}
			current.Content[i+1] = value
			return nil
		}
	}
	if key == nil {
		key = configValueNode(name)
	} else {
		key.Value = name
	}
	current.Content = append(current.Content, key, value)
	return nil
}

// removeConfigPath removes a leaf and returns its original key and value nodes.
// Missing paths return nil nodes and leave the document unchanged. Empty parents
// remain available until migration finishes moving and inspecting their children.
func removeConfigPath(root *yaml.Node, path string) (*yaml.Node, *yaml.Node) {
	parent, name, found := strings.Cut(path, ".")
	if found {
		next := configPath(root, parent)
		if next == nil {
			return nil, nil
		}
		return removeConfigPath(next, name)
	}
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == path {
			key, value := root.Content[i], root.Content[i+1]
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			return key, value
		}
	}
	return nil, nil
}

// moveConfigPath relocates one setting while retaining key and value comments.
// Identical paths and absent source values are no-ops. Earlier specific moves
// may already have created the destination mapping, so disjoint child mappings
// are merged and overlapping scalar values are rejected. Source parent comments
// join the destination container when that merge succeeds. The report records
// only the two path names. Errors can follow earlier in-memory changes, and the
// upgrade framework keeps the source file untouched until every move succeeds.
func moveConfigPath(root *yaml.Node, from, to string, report *upgradeReport) error {
	if from == to || configPath(root, from) == nil {
		return nil
	}
	if destination := configPath(root, to); destination != nil {
		source := configPath(root, from)
		if source.Kind != yaml.MappingNode || destination.Kind != yaml.MappingNode {
			return fmt.Errorf("configuration migration conflict: %s and %s are both present", from, to)
		}
		// Earlier child moves can create this destination container. Merge disjoint
		// children while rejecting any overlapping value so no configuration is overwritten.
		if err := mergeConfigMappings(destination, source, to); err != nil {
			return err
		}
		key, _ := removeConfigPath(root, from)
		parts := strings.Split(to, ".")
		parent := root
		if len(parts) > 1 {
			parent = configPath(root, strings.Join(parts[:len(parts)-1], "."))
		}
		for i := 0; i < len(parent.Content); i += 2 {
			if parent.Content[i].Value == parts[len(parts)-1] {
				parent.Content[i].HeadComment = strings.TrimSpace(key.HeadComment + "\n" + parent.Content[i].HeadComment)
				parent.Content[i].LineComment = strings.TrimSpace(key.LineComment + " " + parent.Content[i].LineComment)
				parent.Content[i].FootComment = strings.TrimSpace(key.FootComment + "\n" + parent.Content[i].FootComment)
				break
			}
		}
		report.Renamed = append(report.Renamed, from+" -> "+to)
		return nil
	}
	key, value := removeConfigPath(root, from)
	if err := putConfigPath(root, to, key, value); err != nil {
		return err
	}
	report.Renamed = append(report.Renamed, from+" -> "+to)
	return nil
}

// pruneEmptyConfigMappings removes mapping containers left empty by deleted keys.
// Nonempty maps and every sequence retain their order and contents; explicit
// empty lists remain meaningful inputs for client-IP and other list policies.
func pruneEmptyConfigMappings(root *yaml.Node) {
	if root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(root.Content); {
		child := root.Content[i+1]
		pruneEmptyConfigMappings(child)
		if child.Kind == yaml.MappingNode && len(child.Content) == 0 {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			continue
		}
		i += 2
	}
}

// expandConfigAliases materializes alias values before versioned key movement.
// Cycles fail before transformation. Merge keys use YAML decoding semantics to
// supply missing values, while explicit keys and their comments retain priority.
//
// Each alias receives separate nodes because renamed paths can have different
// destinations. Anchor labels are cleared only on materialized copies, preventing
// stale aliases after a source subtree disappears. Ordinary documents without
// aliases retain their original key ordering and comments through this operation.
func expandConfigAliases(node *yaml.Node, active map[*yaml.Node]bool) (*yaml.Node, error) {
	if active[node] {
		return nil, fmt.Errorf("configuration contains a cyclic YAML alias")
	}
	active[node] = true
	defer delete(active, node)
	if node.Kind == yaml.AliasNode {
		return expandConfigAliases(node.Alias, active)
	}
	copy := *node
	copy.Anchor = ""
	copy.Content = nil
	for _, child := range node.Content {
		next, err := expandConfigAliases(child, active)
		if err != nil {
			return nil, err
		}
		copy.Content = append(copy.Content, next)
	}
	if copy.Kind == yaml.MappingNode {
		var merges []*yaml.Node
		for i := 0; i < len(copy.Content); {
			if copy.Content[i].Tag != "!!merge" {
				i += 2
				continue
			}
			value := copy.Content[i+1]
			if value.Kind == yaml.SequenceNode {
				merges = append(merges, value.Content...)
			} else {
				merges = append(merges, value)
			}
			copy.Content = append(copy.Content[:i], copy.Content[i+2:]...)
		}
		for _, mapping := range merges {
			if mapping.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("configuration contains an invalid YAML merge")
			}
			for i := 0; i < len(mapping.Content); i += 2 {
				if configMappingValue(&copy, mapping.Content[i].Value) == nil {
					copy.Content = append(copy.Content, mapping.Content[i], mapping.Content[i+1])
				}
			}
		}
	}
	return &copy, nil
}

// mergeConfigMappings appends disjoint fields to a previously created destination.
// Specific child moves can create a container before its remaining parent subtree
// moves, so matching mappings recurse while matching scalar values are conflicts.
// Original key and value nodes are retained for comments and representation.
//
// Earlier siblings can already have been appended when a later conflict fails;
// callers discard that in-memory migration result rather than persisting it.
// The source container is removed only by its owning move after a successful merge.
func mergeConfigMappings(destination, source *yaml.Node, path string) error {
	for i := 0; i < len(source.Content); i += 2 {
		key, value := source.Content[i], source.Content[i+1]
		existing := configMappingValue(destination, key.Value)
		if existing == nil {
			destination.Content = append(destination.Content, key, value)
			continue
		}
		if existing.Kind != yaml.MappingNode || value.Kind != yaml.MappingNode {
			return fmt.Errorf("configuration migration conflict at %s.%s", path, key.Value)
		}
		if err := mergeConfigMappings(existing, value, path+"."+key.Value); err != nil {
			return err
		}
	}
	return nil
}

// canonicalizeV1Paths expands recognized dotted keys and folds static key casing.
// Viper accepted both spellings before versioning, so migration must recognize
// them before alias selection and unknown-key removal. Arbitrary map entries
// retain their original spelling and representation; their decoded values still
// follow the current loader's map rules.
//
// A collision with an already supplied canonical path is rejected instead of
// inventing precedence between duplicate settings. Key nodes move with their
// comments into the canonical tree. Only the in-memory document changes, and
// the source file remains available when a collision stops migration.
func canonicalizeV1Paths(root *yaml.Node, prefix string) error {
	if root.Kind == yaml.SequenceNode && prefix == "security.proxy_probe.urls" {
		for _, entry := range root.Content {
			if entry.Kind != yaml.MappingNode {
				continue
			}
			seen := map[string]bool{}
			for index := 0; index < len(entry.Content); index += 2 {
				name := strings.ToLower(entry.Content[index].Value)
				if name != "url" && name != "parser" {
					continue
				}
				if seen[name] {
					return fmt.Errorf("configuration has duplicate version 1 probe field %s", name)
				}
				seen[name] = true
				entry.Content[index].Value = name
			}
		}
		return nil
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(root.Content); {
		key, value := root.Content[i], root.Content[i+1]
		normalized := strings.ToLower(key.Value)
		full := normalized
		if prefix != "" {
			full = prefix + "." + normalized
		}
		known, openMap := false, false
		for _, entry := range v1Keys {
			if full == entry.Path {
				known = true
				openMap = entry.Kind == kindMap
				break
			}
			if strings.HasPrefix(entry.Path, full+".") {
				known = true
			}
		}
		for _, alias := range v1Aliases {
			if full == alias.Old || strings.HasPrefix(alias.Old, full+".") {
				known = true
			}
		}
		if !known {
			i += 2
			continue
		}
		if normalized != key.Value || strings.Contains(normalized, ".") {
			if configPath(root, normalized) != nil {
				return fmt.Errorf("configuration has duplicate version 1 path %s", full)
			}
			// Remove the alternate spelling before inserting its canonical path so the
			// same key node can retain comments without being attached at two locations.
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			if err := putConfigPath(root, normalized, key, value); err != nil {
				return err
			}
			continue
		}
		if !openMap {
			if err := canonicalizeV1Paths(value, full); err != nil {
				return err
			}
		}
		i += 2
	}
	return nil
}
