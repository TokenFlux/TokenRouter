package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// interpolate.go resolves explicit environment references in configuration values.
// Disk documents retain their reference spelling while loading uses a separate
// node tree. String tags survive interpolation so credentials containing leading
// zeros or YAML-looking text keep their original representation until typed
// configuration decoding. Mapping keys never participate in substitution.
//
// Escaped openings and default text are emitted without recursive expansion.
// Reference discovery shares the parser with substitution so ignored-environment
// diagnostics exempt exactly the variable names used by the file. Errors identify
// paths and names, while neither successful values nor default contents enter
// diagnostics. Aliases are copied with their targets to preserve document ownership.

// interpolateEnvReferences creates the decoded document without changing the
// original YAML nodes. Missing required variables and malformed references fail
// with a path-only diagnostic; an explicitly empty environment value remains valid.
func interpolateEnvReferences(doc *yaml.Node, lookup func(string) (string, bool)) (*yaml.Node, error) {
	copy := cloneConfigNode(doc)
	err := visitConfigStrings(copy, "", func(key string, n *yaml.Node) error {
		value, _, err := expandConfigString(key, n.Value, lookup)
		if err != nil {
			return err
		}
		n.Value = value
		n.Tag = "!!str"
		return nil
	})
	return copy, err
}

// cloneConfigNode copies a YAML node graph while preserving shared alias identity.
// Each original node maps to one copied node, so repeated aliases share their
// copied target without retaining mutable source nodes. The memo is populated
// before children are copied, which also terminates traversal of cyclic graphs.
// This helper preserves graph shape rather than validating it; parsing rejects
// invalid alias cycles before loading. Callers can change copied values and
// comments without altering the document that will remain on disk.
func cloneConfigNode(doc *yaml.Node) *yaml.Node {
	memo := map[*yaml.Node]*yaml.Node{}
	var clone func(*yaml.Node) *yaml.Node
	clone = func(n *yaml.Node) *yaml.Node {
		if n == nil {
			return nil
		}
		if prior := memo[n]; prior != nil {
			return prior
		}
		c := *n
		memo[n] = &c
		c.Content = make([]*yaml.Node, len(n.Content))
		for i, child := range n.Content {
			c.Content[i] = clone(child)
		}
		c.Alias = clone(n.Alias)
		return &c
	}
	return clone(doc)
}

// visitConfigStrings visits string values with their dotted or indexed path.
// Mapping keys remain literal, and sequences add their element index only to
// diagnostics. Aliased targets are visited once: expanding the same target again
// could accidentally interpret reference-looking text introduced by the first
// substitution. The first encountered path therefore owns any diagnostic for a
// shared value. Callers provide a mutable document they own, and a callback error
// stops traversal after any earlier successful value changes.
func visitConfigStrings(doc *yaml.Node, prefix string, visit func(string, *yaml.Node) error) error {
	seen := map[*yaml.Node]bool{}
	var walk func(*yaml.Node, string) error
	walk = func(n *yaml.Node, key string) error {
		if n == nil || seen[n] {
			return nil
		}
		seen[n] = true
		switch n.Kind {
		case yaml.DocumentNode:
			for _, child := range n.Content {
				if err := walk(child, key); err != nil {
					return err
				}
			}
		case yaml.AliasNode:
			return walk(n.Alias, key)
		case yaml.MappingNode:
			for i := 0; i < len(n.Content); i += 2 {
				next := n.Content[i].Value
				if key != "" {
					next = key + "." + next
				}
				if err := walk(n.Content[i+1], next); err != nil {
					return err
				}
			}
		case yaml.SequenceNode:
			for i, child := range n.Content {
				if err := walk(child, fmt.Sprintf("%s[%d]", key, i)); err != nil {
					return err
				}
			}
		case yaml.ScalarNode:
			if n.Tag == "!!str" {
				return visit(key, n)
			}
		}
		return nil
	}
	return walk(doc, prefix)
}

// expandConfigString parses explicit references and returns their names alongside
// substituted text. Defaults and escaped openings are copied literally, so a
// referenced value cannot introduce a second expansion or alter YAML types.
// The parser scans the original input only; unset required names cause an error
// before the caller can decode or log a partially expanded scalar. A nil lookup
// collects names for diagnostics without requiring the deployment's environment.
func expandConfigString(key, raw string, lookup func(string) (string, bool)) (string, []string, error) {
	var out strings.Builder
	var names []string
	for raw != "" {
		if strings.HasPrefix(raw, "$${") {
			_, _ = out.WriteString("${")
			raw = raw[3:]
			continue
		}
		if !strings.HasPrefix(raw, "${") {
			_ = out.WriteByte(raw[0])
			raw = raw[1:]
			continue
		}
		end := configReferenceEnd(raw)
		if end < 0 {
			return "", nil, fmt.Errorf("%s contains an unterminated environment reference", key)
		}
		expression := raw[2:end]
		name, fallback, hasDefault := strings.Cut(expression, ":-")
		if !validEnvironmentName(name) {
			return "", nil, fmt.Errorf("%s contains an invalid environment variable name", key)
		}
		names = append(names, name)
		value, set := "", true
		if lookup != nil {
			value, set = lookup(name)
		}
		if hasDefault && (!set || value == "") {
			value = fallback
			set = true
		}
		if !set {
			return "", nil, fmt.Errorf("%s references unset environment variable %s", key, name)
		}
		_, _ = out.WriteString(value)
		raw = raw[end+1:]
	}
	return out.String(), names, nil
}

// validEnvironmentName reports whether a name matches the portable reference grammar.
func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// configReferenceEnd finds the closing brace of one explicit environment reference.
// Input starts with the opening dollar and brace. Reference-shaped text inside
// a default is balanced to locate the outer end, but its contents are never
// expanded or added to the variable-name inventory. This prevents a present
// outer value from retaining a stray closing brace when its unused fallback
// contains a literal nested reference. Unterminated input returns minus one
// for the caller's field-only syntax diagnostic.
func configReferenceEnd(raw string) int {
	depth := 1
	for index := 2; index < len(raw); index++ {
		// Count a nested fallback opener once, then advance past its brace. The
		// scanner tracks delimiters here without interpreting the nested variable.
		if raw[index] == '$' && index+1 < len(raw) && raw[index+1] == '{' {
			depth++
			index++
			continue
		}
		if raw[index] == '}' {
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}
