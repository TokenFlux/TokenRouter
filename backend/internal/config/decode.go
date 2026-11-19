package config

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// decode.go decodes expanded YAML configuration nodes into typed Config values
// and records explicit file-presence paths. Interpolation tags expanded
// environment references as !!str so credentials with leading zeros stay intact,
// while numeric, boolean, and string-list targets still accept string-tagged
// scalars produced by those references.
//
// Mapping lookups match exact yaml struct tags on structs and preserve literal
// key spelling, casing, and dots on open map fields. Explicit null scalars keep
// preinitialized struct defaults without marking presence flags, whereas empty
// strings, false booleans, and empty sequences overwrite defaults and mark
// their dotted paths as explicitly configured.

// decodeConfigNode materializes YAML aliases and merges in doc, populates non-null
// fields onto cfg, and returns the set of explicitly supplied dotted paths.
// Nil, empty, or null documents leave cfg unchanged with an empty presence map;
// non-mapping roots and incompatible value types return value-free errors.
func decodeConfigNode(doc *yaml.Node, cfg *Config) (map[string]bool, error) {
	present := make(map[string]bool)
	if cfg == nil || doc == nil {
		return present, nil
	}
	expanded, err := expandConfigAliases(doc, map[*yaml.Node]bool{})
	if err != nil {
		return nil, err
	}
	root := expanded
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return present, nil
		}
		root = root.Content[0]
	}
	if root == nil || (root.Kind == yaml.ScalarNode && root.Tag == "!!null") {
		return present, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("configuration root must be a YAML mapping")
	}
	if err := decodeYAMLStruct(root, reflect.ValueOf(cfg).Elem(), "", present); err != nil {
		return nil, err
	}
	return present, nil
}

// decodeYAMLValue decodes one non-alias YAML node into dst according to dst's
// reflected kind. Errors name only the configuration path and expected shape so
// secret scalar values never enter startup diagnostics.
func decodeYAMLValue(node *yaml.Node, dst reflect.Value, path string, present map[string]bool) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		if dst.Kind() == reflect.Pointer {
			dst.SetZero()
		}
		return nil
	}
	if dst.Kind() == reflect.Pointer {
		elem := reflect.New(dst.Type().Elem())
		if err := decodeYAMLValue(node, elem.Elem(), path, present); err != nil {
			return err
		}
		dst.Set(elem)
		return nil
	}
	switch dst.Kind() {
	case reflect.Struct:
		return decodeYAMLStruct(node, dst, path, present)
	case reflect.Map:
		return decodeYAMLMap(node, dst, path, present)
	case reflect.Slice:
		return decodeYAMLSlice(node, dst, path, present)
	default:
		return decodeYAMLScalar(node, dst, path)
	}
}

// decodeYAMLStruct matches YAML mapping entries against exported yaml-tagged
// fields on dst. Null scalars leave non-pointer defaults untouched and unrecorded
// in present, while non-null entries mark their dotted path before decoding.
func decodeYAMLStruct(node *yaml.Node, dst reflect.Value, prefix string, present map[string]bool) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must be a mapping", prefix)
	}
	fields := make(map[string]int, dst.NumField())
	typ := dst.Type()
	for i := range typ.NumField() {
		if name, ok := configFieldTagName(typ.Field(i)); ok {
			fields[name] = i
		}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		fieldIdx, ok := fields[keyNode.Value]
		if !ok {
			continue
		}
		childPath := keyNode.Value
		if prefix != "" {
			childPath = prefix + "." + keyNode.Value
		}
		fieldVal := dst.Field(fieldIdx)
		// Keep preinitialized defaults and leave presence unset when a YAML key is
		// explicitly null; pointer fields still clear to nil.
		if valNode.Kind == yaml.ScalarNode && valNode.Tag == "!!null" {
			if fieldVal.Kind() == reflect.Pointer {
				fieldVal.SetZero()
			}
			continue
		}
		present[childPath] = true
		if err := decodeYAMLValue(valNode, fieldVal, childPath, present); err != nil {
			return err
		}
	}
	return nil
}

// decodeYAMLMap decodes a YAML mapping into a string-keyed Go map, preserving
// deployment-defined key case and literal dots without path splitting.
func decodeYAMLMap(node *yaml.Node, dst reflect.Value, prefix string, present map[string]bool) error {
	if node.Kind != yaml.MappingNode || dst.Type().Key().Kind() != reflect.String {
		return fmt.Errorf("%s must be a string-keyed mapping", prefix)
	}
	out := reflect.MakeMapWithSize(dst.Type(), len(node.Content)/2)
	elemType := dst.Type().Elem()
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyText := node.Content[i].Value
		valNode := node.Content[i+1]
		elemVal := reflect.New(elemType).Elem()
		childPath := keyText
		if prefix != "" {
			childPath = prefix + "." + keyText
		}
		if valNode.Kind != yaml.ScalarNode || valNode.Tag != "!!null" {
			if err := decodeYAMLValue(valNode, elemVal, childPath, present); err != nil {
				return err
			}
		}
		out.SetMapIndex(reflect.ValueOf(keyText), elemVal)
	}
	dst.Set(out)
	return nil
}

// decodeYAMLSlice decodes a YAML sequence into dst, or splits a string-tagged
// scalar on commas when dst is a []string populated by environment interpolation.
func decodeYAMLSlice(node *yaml.Node, dst reflect.Value, prefix string, present map[string]bool) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && dst.Type().Elem().Kind() == reflect.String {
		// Match historical comma-separated environment list expansion: an empty
		// interpolated string produces an allocated empty slice rather than [""].
		if node.Value == "" {
			dst.Set(reflect.MakeSlice(dst.Type(), 0, 0))
			return nil
		}
		parts := strings.Split(node.Value, ",")
		out := reflect.MakeSlice(dst.Type(), len(parts), len(parts))
		for i, part := range parts {
			out.Index(i).SetString(part)
		}
		dst.Set(out)
		return nil
	}
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s must be a sequence", prefix)
	}
	out := reflect.MakeSlice(dst.Type(), len(node.Content), len(node.Content))
	for i, child := range node.Content {
		elemPath := fmt.Sprintf("%s[%d]", prefix, i)
		if err := decodeYAMLValue(child, out.Index(i), elemPath, present); err != nil {
			return err
		}
	}
	dst.Set(out)
	return nil
}

// decodeYAMLScalar decodes a YAML scalar node into a string, boolean, integer,
// unsigned integer, or floating-point field. It accepts string-tagged scalars
// produced by ${VAR} interpolation while keeping leading-zero strings intact,
// and returns value-free path diagnostics when parsing or integer bounds fail.
func decodeYAMLScalar(node *yaml.Node, dst reflect.Value, path string) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("%s must be a scalar value", path)
	}
	switch dst.Kind() {
	case reflect.String:
		if node.Tag == "!!str" {
			dst.SetString(node.Value)
			return nil
		}
		var text string
		if err := node.Decode(&text); err != nil {
			return fmt.Errorf("%s must be a string", path)
		}
		dst.SetString(text)
		return nil

	case reflect.Bool:
		if node.Tag == "!!bool" {
			var flag bool
			if err := node.Decode(&flag); err != nil {
				return fmt.Errorf("%s must be a boolean", path)
			}
			dst.SetBool(flag)
			return nil
		}
		if node.Tag == "!!str" {
			if node.Value == "" {
				dst.SetBool(false)
				return nil
			}
			flag, err := strconv.ParseBool(strings.TrimSpace(node.Value))
			if err != nil {
				return fmt.Errorf("%s must be a boolean", path)
			}
			dst.SetBool(flag)
			return nil
		}
		// Coerce numeric YAML scalars to booleans by nonzero value so upgraded v1
		// configuration files using 0 or 1 flags retain their prior behavior.
		if node.Tag == "!!int" {
			integer, err := strconv.ParseInt(node.Value, 0, 64)
			if err != nil {
				return fmt.Errorf("%s must be a boolean", path)
			}
			dst.SetBool(integer != 0)
			return nil
		}
		if node.Tag == "!!float" {
			number, err := strconv.ParseFloat(node.Value, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return fmt.Errorf("%s must be a boolean", path)
			}
			dst.SetBool(number != 0)
			return nil
		}
		return fmt.Errorf("%s must be a boolean", path)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bits := dst.Type().Bits()
		if node.Tag == "!!int" {
			var integer int64
			if err := node.Decode(&integer); err != nil || dst.OverflowInt(integer) {
				return fmt.Errorf("%s must be an integer", path)
			}
			dst.SetInt(integer)
			return nil
		}
		if node.Tag == "!!str" {
			trimmed := strings.TrimSpace(node.Value)
			if trimmed == "" {
				dst.SetInt(0)
				return nil
			}
			integer, err := strconv.ParseInt(trimmed, 0, bits)
			if err != nil {
				return fmt.Errorf("%s must be an integer", path)
			}
			dst.SetInt(integer)
			return nil
		}
		return fmt.Errorf("%s must be an integer", path)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		bits := dst.Type().Bits()
		if node.Tag == "!!int" {
			var unsigned uint64
			if err := node.Decode(&unsigned); err != nil || dst.OverflowUint(unsigned) {
				return fmt.Errorf("%s must be an unsigned integer", path)
			}
			dst.SetUint(unsigned)
			return nil
		}
		if node.Tag == "!!str" {
			trimmed := strings.TrimSpace(node.Value)
			if trimmed == "" {
				dst.SetUint(0)
				return nil
			}
			unsigned, err := strconv.ParseUint(trimmed, 0, bits)
			if err != nil {
				return fmt.Errorf("%s must be an unsigned integer", path)
			}
			dst.SetUint(unsigned)
			return nil
		}
		return fmt.Errorf("%s must be an unsigned integer", path)

	case reflect.Float32, reflect.Float64:
		bits := dst.Type().Bits()
		if node.Tag == "!!float" || node.Tag == "!!int" {
			var number float64
			if err := node.Decode(&number); err != nil || dst.OverflowFloat(number) {
				return fmt.Errorf("%s must be a number", path)
			}
			dst.SetFloat(number)
			return nil
		}
		if node.Tag == "!!str" {
			trimmed := strings.TrimSpace(node.Value)
			if trimmed == "" {
				dst.SetFloat(0)
				return nil
			}
			number, err := strconv.ParseFloat(trimmed, bits)
			if err != nil {
				return fmt.Errorf("%s must be a number", path)
			}
			dst.SetFloat(number)
			return nil
		}
		return fmt.Errorf("%s must be a number", path)
	}
	return fmt.Errorf("%s has an unsupported configuration field kind", path)
}
