package adminapi

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// schema.go describes management arguments using the HTTP request types that
// handlers already decode. Gin binding tags determine required properties;
// JSON omission tags alone do not describe whether an HTTP input is required.
// Pointer values accept explicit null, while omitted properties retain the
// handler's existing partial-update behavior.
//
// Path and query arguments remain separate string-valued objects because Gin
// reads both from URL text. Body values retain their JSON types. Dynamic maps
// accept their declared value type and keep validation of platform-specific
// keys with the existing handler. Recursive types stop at an unconstrained
// schema rather than recursing indefinitely, while surrounding object fields
// and binding requirements remain available to the client and validator.

// Arguments describes the path, query, and optional JSON body accepted by one
// operation. Nil body metadata declares a bodyless request; typed nil pointers
// identify the existing request DTO. Explicit query names include parameters
// read indirectly by pagination helpers.
//
// Path parameters remain required strings, while Go binding tags determine
// required body properties. This separation preserves handler defaults for
// omitted fields and allows explicit null only where the reflected request
// shape supports it. The existing handler still owns domain validation.
func Arguments(route string, query []string, body any) *jsonschema.Schema {
	root := objectSchema()
	parameters := objectSchema()
	for segment := range strings.SplitSeq(route, "/") {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			name := segment[1:]
			parameters.Properties[name] = &jsonschema.Schema{Type: "string", MinLength: intPointer(1)}
			parameters.Required = append(parameters.Required, name)
		}
	}
	root.Properties["path"] = parameters
	if len(parameters.Required) > 0 {
		root.Required = append(root.Required, "path")
	}
	queries := objectSchema()
	queries.Properties["timezone"] = &jsonschema.Schema{Type: "string", Description: "Optional IANA timezone for endpoints that consume time-based query filters; fixed-timezone endpoints retain their configured calendar."}
	for _, name := range query {
		queries.Properties[name] = &jsonschema.Schema{Type: "string"}
	}
	root.Properties["query"] = queries
	if body != nil {
		bodyType := reflect.TypeOf(body)
		if bodyType.Kind() == reflect.Pointer {
			bodyType = bodyType.Elem()
		}
		bodySchema := requestSchema(bodyType, make(map[reflect.Type]bool))
		root.Properties["body"] = bodySchema
		if len(bodySchema.Required) > 0 {
			root.Required = append(root.Required, "body")
		}
	}
	return root
}

// objectSchema creates an empty object that rejects undeclared input fields.
func objectSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}

// intPointer supplies an independent integer address for schema bounds.
func intPointer(value int) *int {
	return &value
}

// requestSchema derives a JSON shape from the existing HTTP input type. It
// follows JSON names and Gin required tags, allows null for pointer fields,
// and keeps dynamic maps open to their declared value type. Custom JSON
// decoders receive an unconstrained shape because their private Go
// representation may differ from accepted wire values.
//
// The visited set belongs to one traversal branch. Recursive edges stop
// expanding, while repeated sibling types expand independently so a preceding
// field cannot erase another field's schema.
func requestSchema(t reflect.Type, visited map[reflect.Type]bool) *jsonschema.Schema {
	if visited[t] || t == reflect.TypeFor[json.RawMessage]() {
		return &jsonschema.Schema{}
	}
	if t == reflect.TypeFor[json.Number]() {
		// json.Number retains decimal text internally but emits a JSON number, so its reflected string kind would misdescribe numeric capability limits.
		return &jsonschema.Schema{Type: "number"}
	}
	if t == reflect.TypeFor[time.Time]() {
		return &jsonschema.Schema{Type: "string", Format: "date-time"}
	}
	if t.Kind() == reflect.Struct && reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		// Custom decoders can accept scalars or preserve field presence in private
		// fields; their Go struct layout cannot describe the accepted wire representation.
		return &jsonschema.Schema{Description: "Custom JSON value interpreted and validated by the HTTP handler."}
	}
	if t.Kind() == reflect.Pointer {
		return &jsonschema.Schema{AnyOf: []*jsonschema.Schema{requestSchema(t.Elem(), visited), {Type: "null"}}}
	}
	visited[t] = true
	defer delete(visited, t)
	switch t.Kind() {
	case reflect.Struct:
		schema := objectSchema()
		for field := range t.Fields() {
			if field.PkgPath != "" || field.Tag.Get("json") == "-" {
				continue
			}
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			property := requestSchema(field.Type, visited)
			if field.Anonymous && name == "" {
				maps.Copy(schema.Properties, property.Properties)
				schema.Required = append(schema.Required, property.Required...)
				continue
			}
			if name == "" {
				name = field.Name
			}
			if description := field.Tag.Get("jsonschema"); description != "" {
				property.Description = description
			}
			omitEmpty := false
			for rule := range strings.SplitSeq(field.Tag.Get("binding"), ",") {
				if rule == "omitempty" {
					omitEmpty = true
				}
				if rule == "required" {
					schema.Required = append(schema.Required, name)
				}
				if values, ok := strings.CutPrefix(rule, "oneof="); ok && field.Type.Kind() == reflect.String {
					// Gin skips subsequent rules for a zero value only after omitempty;
					// retaining that empty string keeps partial-update defaults reachable through MCP.
					if omitEmpty {
						property.Enum = append(property.Enum, "")
					}
					for value := range strings.FieldsSeq(values) {
						property.Enum = append(property.Enum, value)
					}
				}
			}
			schema.Properties[name] = property
		}
		return schema
	case reflect.Map:
		return &jsonschema.Schema{Types: []string{"object", "null"}, AdditionalProperties: requestSchema(t.Elem(), visited)}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return &jsonschema.Schema{Types: []string{"string", "null"}, Description: "Base64 encoded bytes."}
		}
		// JSON null decodes to nil slices and maps in the existing HTTP binders.
		// Retain that input alongside empty collections so partial updates reach the same business rules.
		return &jsonschema.Schema{Types: []string{"array", "null"}, Items: requestSchema(t.Elem(), visited)}
	case reflect.String:
		return &jsonschema.Schema{Type: "string"}
	case reflect.Bool:
		return &jsonschema.Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &jsonschema.Schema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return &jsonschema.Schema{Type: "number"}
	default:
		return &jsonschema.Schema{}
	}
}

// JSONResponse describes the shared HTTP envelope and an optional concrete data type.
// Dynamic responses leave data unconstrained; known DTOs reuse their JSON field
// shapes. MCP execution adds transport status and can apply field redaction
// to this underlying HTTP response according to the credential's settings.
func JSONResponse(body any) *jsonschema.Schema {
	data := &jsonschema.Schema{}
	if body != nil {
		data = requestSchema(reflect.TypeOf(body), make(map[reflect.Type]bool))
	}
	return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
		"code": {Type: "integer"}, "message": {Type: "string"}, "data": data,
	}, Description: "Original HTTP JSON response. Dynamic data and additional response fields are described by the operation; inspect returned business states for batch or asynchronous actions."}
}

// OpenAPI returns the same mounted operations as an OpenAPI 3.1 document.
// Extensions retain permission and interaction metadata; callers filter the
// catalogue first when publishing a credential-specific view of these descriptions.
func (c *Catalog) OpenAPI() map[string]any {
	paths := map[string]any{}
	for _, op := range c.ordered {
		segments := strings.Split(op.Path, "/")
		for i, segment := range segments {
			if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
				segments[i] = "{" + segment[1:] + "}"
			}
		}
		key := strings.Join(segments, "/")
		item, _ := paths[key].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[key] = item
		}
		var parameters []map[string]any
		for _, location := range []string{"path", "query"} {
			for _, name := range slices.Sorted(maps.Keys(op.Input.Properties[location].Properties)) {
				schema := op.Input.Properties[location].Properties[name]
				parameters = append(parameters, map[string]any{"name": name, "in": location, "required": location == "path", "schema": schema})
			}
		}
		entry := map[string]any{"operationId": op.ID, "summary": op.Title, "description": op.Description, "tags": []string{op.Module}, "parameters": parameters, "responses": map[string]any{strconv.Itoa(200): map[string]any{"description": "Management operation result"}}, "x-tokenrouter-read-only": op.ReadOnly, "x-tokenrouter-confirmation": op.Sensitive, "x-tokenrouter-response": op.Response}
		if op.Alias != "" {
			entry["x-tokenrouter-mcp-alias"] = op.Alias
		}
		if op.StepUpWhen != nil {
			entry["x-tokenrouter-step-up-when"] = op.StepUpWhen
		}
		if op.HTTPUpgrade != "" {
			entry["responses"] = map[string]any{"101": map[string]any{
				"description": "Switch protocols to " + op.HTTPUpgrade,
				"headers":     map[string]any{"Upgrade": map[string]any{"schema": map[string]any{"type": "string", "enum": []string{op.HTTPUpgrade}}}},
			}}
			entry["x-tokenrouter-http-upgrade"] = op.HTTPUpgrade
		} else if op.Output != nil {
			entry["responses"] = map[string]any{"200": map[string]any{"description": "Management operation result", "content": map[string]any{"application/json": map[string]any{"schema": op.Output}}}}
		}
		if body := op.Input.Properties["body"]; body != nil {
			entry["requestBody"] = map[string]any{"required": len(body.Required) > 0, "content": map[string]any{"application/json": map[string]any{"schema": body}}}
		}
		if op.ID == "mcp.confirmations.get" || op.Response == ResponseBrowser && (op.Module == "mcp" || op.ID == "audit_logs.clear.create") {
			entry["security"] = []map[string]any{{"adminSession": []string{}}}
		}
		item[strings.ToLower(op.Method)] = entry
	}
	return map[string]any{
		"openapi":  "3.1.0",
		"info":     map[string]any{"title": "TokenRouter management API", "version": "1"},
		"paths":    paths,
		"security": []map[string]any{{"adminSession": []string{}}, {"adminKey": []string{}}},
		"components": map[string]any{"securitySchemes": map[string]any{
			"adminSession": map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"},
			"adminKey":     map[string]any{"type": "apiKey", "in": "header", "name": "x-api-key"},
		}},
	}
}
