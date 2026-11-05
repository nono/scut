// Package spec gives a compact view of the operations and schemas of the v4
// OpenAPI spec. See docs/design/2026-11-05-spec-design.md.
package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// workspacePrefix is the part of a path that scut api adds itself.
const workspacePrefix = "/api/v4/{workspace-slug}/"

// generated matches the schema names that Shortcut renumbers on each publish.
var generated = regexp.MustCompile(`^(Body|Response)[0-9]+$`)

type obj = map[string]any

// Spec is a parsed OpenAPI document.
type Spec struct {
	components map[string]obj // "schemas", "requestBodies", "responses"
	ops        map[string]op
}

type op struct {
	method, path string
	body         obj
}

// Operation is the compact view of one operation.
type Operation struct {
	Operation  string            `json:"operation"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Summary    string            `json:"summary,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	Body       any               `json:"body,omitempty"`
	Response   any               `json:"response,omitempty"`
}

// Schema is the compact view of one named schema.
type Schema struct {
	Schema string `json:"schema"`
	Shape  any    `json:"shape"`
}

// Parse reads an OpenAPI 3.0 document.
func Parse(b []byte) (*Spec, error) {
	var doc struct {
		Paths      map[string]map[string]obj `json:"paths"`
		Components map[string]obj            `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}
	s := &Spec{components: doc.Components, ops: map[string]op{}}
	for path, item := range doc.Paths {
		for method, o := range item {
			if id, _ := o["operationId"].(string); id != "" {
				s.ops[id] = op{method: strings.ToUpper(method), path: path, body: o}
			}
		}
	}
	if len(s.ops) == 0 {
		return nil, fmt.Errorf("spec: no operations")
	}
	return s, nil
}

// Lookup returns the Operation of an operationId, else the Schema of a
// schema name. Named schemas are inline up to depth levels.
func (s *Spec) Lookup(name string, depth int) (any, bool) {
	if o, ok := s.ops[name]; ok {
		return s.operation(name, o, depth), true
	}
	if _, ok := s.components["schemas"][name]; ok {
		ref := obj{"$ref": "#/components/schemas/" + name}
		return Schema{Schema: name, Shape: s.compact(ref, depth, map[string]bool{})}, true
	}
	return nil, false
}

func (s *Spec) operation(id string, o op, depth int) Operation {
	out := Operation{Operation: id, Method: o.method, Path: o.path}
	if rel, ok := strings.CutPrefix(o.path, workspacePrefix); ok {
		out.Path = rel
	}
	out.Summary, _ = o.body["summary"].(string)
	params, _ := o.body["parameters"].([]any)
	for _, p := range params {
		p, _ := p.(obj)
		name, _ := p["name"].(string)
		if name == "" || name == "workspace-slug" {
			continue
		}
		if req, _ := p["required"].(bool); !req {
			name += "?"
		}
		if out.Parameters == nil {
			out.Parameters = map[string]string{}
		}
		in, _ := p["in"].(string)
		out.Parameters[name] = in + " " + fmt.Sprint(s.compact(asObj(p["schema"]), 0, map[string]bool{}))
	}
	if rb := s.content(asObj(o.body["requestBody"]), "requestBodies"); rb != nil {
		out.Body = s.compact(rb, depth, map[string]bool{})
	}
	if r := s.content(s.success(asObj(o.body["responses"])), "responses"); r != nil {
		out.Response = s.compact(r, depth, map[string]bool{})
	}
	return out
}

// success returns the first 2xx response.
func (s *Spec) success(responses obj) obj {
	codes := make([]string, 0, len(responses))
	for c := range responses {
		if strings.HasPrefix(c, "2") {
			codes = append(codes, c)
		}
	}
	if len(codes) == 0 {
		return nil
	}
	sort.Strings(codes)
	return asObj(responses[codes[0]])
}

// content resolves a requestBody or response (inline or a ref to the
// components of kind) to its JSON schema. It is nil when there is no body.
func (s *Spec) content(n obj, kind string) obj {
	if ref, ok := n["$ref"].(string); ok {
		n = asObj(s.components[kind][strings.TrimPrefix(ref, "#/components/"+kind+"/")])
	}
	c := asObj(asObj(n["content"])["application/json"])
	return asObj(c["schema"])
}

func asObj(v any) obj {
	o, _ := v.(obj)
	return o
}

// compact returns the compact form of a schema node. open holds the named
// schemas on the current path, so a cycle ends.
func (s *Spec) compact(n obj, depth int, open map[string]bool) any {
	var v any
	switch {
	case n["$ref"] != nil:
		v = s.ref(n["$ref"].(string), depth, open)
	case n["oneOf"] != nil:
		v = obj{"oneOf": s.list(n["oneOf"], depth, open)}
	case n["anyOf"] != nil:
		v = obj{"oneOf": s.list(n["anyOf"], depth, open)}
	case n["allOf"] != nil:
		v = s.allOf(s.list(n["allOf"], depth, open))
	case n["enum"] != nil:
		var vals []string
		for _, e := range n["enum"].([]any) {
			vals = append(vals, fmt.Sprint(e))
		}
		v = strings.Join(vals, "|")
	case n["type"] == "array":
		v = []any{s.compact(asObj(n["items"]), depth, open)}
	case n["type"] == "object" || n["properties"] != nil:
		v = s.object(n, depth, open)
	case n["type"] != nil:
		t := fmt.Sprint(n["type"])
		if f, ok := n["format"].(string); ok {
			t += "(" + f + ")"
		}
		v = t
	default:
		v = "any"
	}
	if nullable, _ := n["nullable"].(bool); nullable {
		if str, ok := v.(string); ok {
			return str + "|null"
		}
		return obj{"oneOf": []any{v, "null"}}
	}
	return v
}

func (s *Spec) ref(ref string, depth int, open map[string]bool) any {
	name := strings.TrimPrefix(ref, "#/components/schemas/")
	target, ok := s.components["schemas"][name].(obj)
	if !ok {
		return "$" + name
	}
	if !generated.MatchString(name) {
		if open[name] || depth == 0 {
			return "$" + name
		}
		depth--
	}
	open[name] = true
	defer delete(open, name)
	return s.compact(target, depth, open)
}

func (s *Spec) list(v any, depth int, open map[string]bool) []any {
	items, _ := v.([]any)
	out := make([]any, 0, len(items))
	for _, it := range items {
		out = append(out, s.compact(asObj(it), depth, open))
	}
	return out
}

// allOf merges the objects of an allOf. If one part is not an object, the
// parts are kept as a list.
func (s *Spec) allOf(parts []any) any {
	m := obj{}
	for _, p := range parts {
		o, ok := p.(obj)
		if !ok {
			return obj{"allOf": parts}
		}
		for k, v := range o {
			m[k] = v
		}
	}
	return m
}

func (s *Spec) object(n obj, depth int, open map[string]bool) any {
	req := map[string]bool{}
	if r, ok := n["required"].([]any); ok {
		for _, k := range r {
			req[fmt.Sprint(k)] = true
		}
	}
	m := obj{}
	for k, p := range asObj(n["properties"]) {
		key := k
		if !req[k] {
			key += "?"
		}
		m[key] = s.compact(asObj(p), depth, open)
	}
	switch ap := n["additionalProperties"].(type) {
	case bool:
		if ap {
			m["*"] = "any"
		}
	case obj:
		if len(ap) > 0 {
			m["*"] = s.compact(ap, depth, open)
		}
	}
	if len(m) == 0 {
		return "object"
	}
	return m
}
