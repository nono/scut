package spec

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nono/scut/api"
)

const fixture = `{
  "paths": {
    "/api/v4/{workspace-slug}/things/{id}": {
      "patch": {
        "operationId": "updateThing",
        "summary": "Update Thing",
        "parameters": [
          {"in": "path", "name": "workspace-slug", "required": true, "schema": {"type": "string"}},
          {"in": "path", "name": "id", "required": true, "schema": {"type": "integer", "format": "int64"}},
          {"in": "query", "name": "fields", "schema": {"type": "string"}}
        ],
        "requestBody": {"$ref": "#/components/requestBodies/Body1"},
        "responses": {
          "400": {"$ref": "#/components/responses/Error"},
          "201": {"$ref": "#/components/responses/Other"},
          "200": {"$ref": "#/components/responses/ThingWrapper"}
        }
      },
      "get": {
        "operationId": "getThing",
        "responses": {"404": {"description": "Not Found"}}
      },
      "delete": {
        "operationId": "deleteThing",
        "responses": {"204": {"description": "No Content"}}
      }
    },
    "/api/v4/whoami": {
      "get": {
        "operationId": "getWhoami",
        "responses": {"200": {"content": {"application/json": {"schema": {"type": "string"}}}}}
      }
    }
  },
  "components": {
    "requestBodies": {
      "Body1": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Body1"}}}}
    },
    "responses": {
      "ThingWrapper": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/ThingWrapper"}}}},
      "Other": {"content": {"application/json": {"schema": {"type": "boolean"}}}},
      "Error": {"content": {"application/json": {"schema": {"type": "number"}}}}
    },
    "schemas": {
      "Body1": {"type": "object", "required": ["name"], "properties": {
        "name": {"type": "string"},
        "kind": {"type": "string", "enum": ["a", "b c"]},
        "due": {"type": "string", "format": "date-time", "nullable": true},
        "parent": {"$ref": "#/components/schemas/Thing", "nullable": true}
      }},
      "ThingWrapper": {"type": "object", "properties": {"entity": {"$ref": "#/components/schemas/Thing"}}},
      "Thing": {"type": "object", "additionalProperties": false, "properties": {
        "id": {"type": "integer"},
        "tags": {"type": "array", "items": {"$ref": "#/components/schemas/Tag"}},
        "parent": {"$ref": "#/components/schemas/Thing"},
        "ghost": {"$ref": "#/components/schemas/Missing"}
      }},
      "Tag": {"type": "object", "properties": {"label": {"type": "string"}}}
    }
  }
}`

func parse(t *testing.T) *Spec {
	t.Helper()
	sp, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

// norm turns v into its JSON form, so tests compare plain maps and lists.
func norm(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func eq(t *testing.T, got any, want string) {
	t.Helper()
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if g := norm(t, got); !reflect.DeepEqual(g, w) {
		gb, _ := json.Marshal(g)
		t.Errorf("got  %s\nwant %s", gb, want)
	}
}

func TestOperation(t *testing.T) {
	v, ok := parse(t).Lookup("updateThing", 2)
	if !ok {
		t.Fatal("not found")
	}
	eq(t, v, `{
	  "operation": "updateThing", "method": "PATCH", "path": "things/{id}", "summary": "Update Thing",
	  "parameters": {"id": "path integer(int64)", "fields?": "query string"},
	  "body": {"name": "string", "kind?": "a|b c", "due?": "string(date-time)|null", "parent?": {"oneOf": [
	    {"id?": "integer", "tags?": [{"label?": "string"}], "parent?": "$Thing", "ghost?": "$Missing"}, "null"]}},
	  "response": {"entity?": {"id?": "integer", "tags?": ["$Tag"], "parent?": "$Thing", "ghost?": "$Missing"}}
	}`)
}

func TestOperationDepth(t *testing.T) {
	sp := parse(t)
	v, _ := sp.Lookup("updateThing", 0)
	o := v.(Operation)
	// Body1 is a generated name: it is inline at depth 0. ThingWrapper is not.
	eq(t, o.Body, `{"name": "string", "kind?": "a|b c", "due?": "string(date-time)|null", "parent?": "$Thing|null"}`)
	eq(t, o.Response, `"$ThingWrapper"`)
	v, _ = sp.Lookup("updateThing", 1)
	eq(t, v.(Operation).Response, `{"entity?": "$Thing"}`)
}

func TestOperationAbsolutePathAndNoBody(t *testing.T) {
	sp := parse(t)
	v, _ := sp.Lookup("getWhoami", 2)
	eq(t, v, `{"operation": "getWhoami", "method": "GET", "path": "/api/v4/whoami", "response": "string"}`)
	v, _ = sp.Lookup("deleteThing", 2)
	eq(t, v, `{"operation": "deleteThing", "method": "DELETE", "path": "things/{id}"}`)
	v, _ = sp.Lookup("getThing", 2)
	eq(t, v, `{"operation": "getThing", "method": "GET", "path": "things/{id}"}`)
}

func TestSchema(t *testing.T) {
	sp := parse(t)
	v, ok := sp.Lookup("Tag", 2)
	if !ok {
		t.Fatal("not found")
	}
	eq(t, v, `{"schema": "Tag", "shape": {"label?": "string"}}`)
	v, _ = sp.Lookup("Tag", 0)
	eq(t, v, `{"schema": "Tag", "shape": "$Tag"}`)
	if _, ok := sp.Lookup("Nope", 2); ok {
		t.Error("found an unknown name")
	}
}

func TestCompact(t *testing.T) {
	sp := parse(t)
	for _, c := range []struct{ in, want string }{
		{`{"type": "integer", "format": "int64"}`, `"integer(int64)"`},
		{`{"enum": ["x", "y"]}`, `"x|y"`},
		{`{"type": "array", "items": {"type": "string"}, "nullable": true}`, `{"oneOf": [["string"], "null"]}`},
		{`{"type": "object"}`, `"object"`},
		{`{"type": "object", "additionalProperties": {}}`, `"object"`},
		{`{"type": "object", "additionalProperties": true}`, `{"*": "any"}`},
		{`{"type": "object", "additionalProperties": {"type": "integer"}}`, `{"*": "integer"}`},
		{`{"properties": {"a": {"type": "string"}}}`, `{"a?": "string"}`},
		{`{}`, `"any"`},
		{`{"oneOf": [{"type": "string"}, {"$ref": "#/components/schemas/Tag"}]}`, `{"oneOf": ["string", {"label?": "string"}]}`},
		{`{"anyOf": [{"type": "string"}]}`, `{"oneOf": ["string"]}`},
		{`{"allOf": [{"$ref": "#/components/schemas/Tag"}, {"properties": {"b": {"type": "boolean"}}, "required": ["b"]}]}`, `{"label?": "string", "b": "boolean"}`},
		{`{"allOf": [{"type": "string"}, {"type": "object"}]}`, `{"allOf": ["string", "object"]}`},
	} {
		var n obj
		if err := json.Unmarshal([]byte(c.in), &n); err != nil {
			t.Fatal(err)
		}
		t.Run(c.in, func(t *testing.T) { eq(t, sp.compact(n, 2, map[string]bool{}), c.want) })
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("{")); err == nil {
		t.Error("want an error for bad JSON")
	}
	if _, err := Parse([]byte(`{"paths": {}}`)); err == nil {
		t.Error("want an error for a spec with no operations")
	}
}

// TestRealSpec checks the embedded v4 spec, which scut spec reads.
func TestRealSpec(t *testing.T) {
	sp, err := Parse(api.Spec)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := sp.Lookup("updateStory", 2)
	if !ok {
		t.Fatal("no updateStory")
	}
	o := v.(Operation)
	if o.Method != "PATCH" || o.Path != "stories/{story-public-id}" {
		t.Errorf("method %s path %s", o.Method, o.Path)
	}
	body, _ := norm(t, o.Body).(map[string]any)
	if body["owner_ids_add?"] == nil || body["description?"] != "string" {
		t.Errorf("body %v", body)
	}
	resp, _ := norm(t, o.Response).(map[string]any)
	entity, _ := resp["entity"].(map[string]any)
	if entity["workflow_state?"] != "$WorkflowStateSlim" {
		t.Errorf("entity %v", entity)
	}
}
