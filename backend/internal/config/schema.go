package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// schema.go checks configuration paths against the decoded Go structure.
// Mapping fields permit deployment-defined child names, while structured lists
// validate each element using its field schema. Version 2 rejects misspellings
// before interpolation so an unknown key cannot trigger environment access or
// hide an otherwise ignored setting.

// knownConfigKeys returns the finite paths accepted by the current Config type.
// Map values remain open-ended; their parent is included without enumerating
// arbitrary names. Structured-list child paths describe fields within every item.
func knownConfigKeys() map[string]reflect.Type {
	keys := map[string]reflect.Type{}
	collectConfigKeys(reflect.TypeFor[Config](), "", keys)
	return keys
}

// collectConfigKeys records exported decoded fields and recursively describes containers.
// Private fields and excluded tags stay outside the schema. Pointer wrappers
// do not change path identity; maps terminate traversal at their own key.
func collectConfigKeys(typ reflect.Type, prefix string, out map[string]reflect.Type) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	for field := range typ.Fields() {
		if field.PkgPath != "" {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("mapstructure"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		value := field.Type
		for value.Kind() == reflect.Pointer {
			value = value.Elem()
		}
		out[key] = value
		if value.Kind() == reflect.Struct {
			collectConfigKeys(value, key, out)
		}
		if value.Kind() == reflect.Slice && value.Elem().Kind() == reflect.Struct {
			collectConfigKeys(value.Elem(), key, out)
		}
	}
}

// checkUnknownKeys reports every unknown configuration path in sorted order.
// Historical spellings include relocation or removal guidance. Map contents
// stay open-ended, and list indices appear only in diagnostics rather than in
// the canonical key used for schema lookup.
func checkUnknownKeys(doc *yaml.Node) error {
	if doc == nil {
		return nil
	}
	expanded, err := expandConfigAliases(doc, map[*yaml.Node]bool{})
	if err != nil {
		return err
	}
	doc = expanded
	keys := knownConfigKeys()
	var unknown []string
	var walk func(*yaml.Node, string, string)
	walk = func(node *yaml.Node, prefix, display string) {
		if node.Kind == yaml.DocumentNode {
			for _, child := range node.Content {
				walk(child, prefix, display)
			}
			return
		}
		if node.Kind == yaml.AliasNode {
			walk(node.Alias, prefix, display)
			return
		}
		if node.Kind == yaml.SequenceNode {
			for i, child := range node.Content {
				walk(child, prefix, fmt.Sprintf("%s[%d]", display, i))
			}
			return
		}
		if node.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i < len(node.Content); i += 2 {
			name := node.Content[i].Value
			key, shown := name, name
			if prefix != "" {
				key = prefix + "." + name
				shown = display + "." + name
			}
			typ, ok := keys[key]
			if !ok {
				if child := node.Content[i+1]; child.Kind == yaml.MappingNode && len(child.Content) > 0 {
					walk(child, key, shown)
				} else {
					unknown = append(unknown, shown+unknownConfigHint(key))
				}
				continue
			}
			if typ.Kind() != reflect.Map {
				walk(node.Content[i+1], key, shown)
			}
		}
	}
	walk(doc, "", "")
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return fmt.Errorf("unknown configuration keys: %s", strings.Join(unknown, ", "))
}

// unknownConfigHint supplies value-free guidance for historical keys and containers.
func unknownConfigHint(key string) string {
	for removed, reason := range v2Removed {
		if key == removed || strings.HasPrefix(key, removed+".") {
			return " (removed in config_version 2: " + reason + ")"
		}
	}
	for _, move := range v2Moves {
		if move.Old == move.New {
			continue
		}
		if key == move.Old || strings.HasPrefix(key, move.Old+".") {
			return " (renamed to " + move.New + strings.TrimPrefix(key, move.Old) + " in config_version 2)"
		}
	}
	for _, alias := range v1Aliases {
		if alias.Old == key {
			return unknownConfigHint(alias.New)
		}
	}
	return ""
}
