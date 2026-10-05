// Package docs holds no code. These tests exist so that the hand-maintained
// observed-behaviour OpenAPI document cannot silently rot: it is edited by
// hand whenever the live API surprises us, and a stray comma — or a response
// missing its required description — would otherwise go unnoticed until
// someone fed the file to a generator.
//
// The structural rules below are the ones an OpenAPI validator would enforce.
// No such validator is reachable from this environment, so they are
// implemented here, which has the advantage of running in CI on every change
// rather than once by hand.
//
// validateSpec returns its findings instead of calling t.Error, so that the
// validator itself can be tested against deliberately broken documents. A
// validator that has only ever been run against a file that passes is not
// known to detect anything.
package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const observedSpec = "openapi-observed.json"

func loadSpec(t *testing.T) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(observedSpec)
	if err != nil {
		t.Fatalf("cannot read %s: %v", observedSpec, err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v", observedSpec, err)
	}
	return doc
}

// resolveRef follows a local JSON pointer such as
// "#/components/responses/RateLimited" and returns what it points at.
func resolveRef(doc map[string]interface{}, ref string) (interface{}, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("non-local $ref %q: this document must be self-contained", ref)
	}
	var cur interface{} = doc
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("$ref %q: segment %q is not an object", ref, seg)
		}
		cur, ok = m[seg]
		if !ok {
			return nil, fmt.Errorf("$ref %q does not resolve: missing %q", ref, seg)
		}
	}
	return cur, nil
}

// httpMethods are the operation keys OpenAPI 3.0 recognises on a Path Item.
// Anything else at that level is either a sibling field or a typo.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// pathItemFields are the non-operation keys a Path Item may carry.
var pathItemFields = map[string]bool{
	"summary": true, "description": true, "servers": true, "parameters": true, "$ref": true,
}

// validateSpec reports every structural problem it finds, in no particular
// order. An empty result means the document satisfies the rules checked here.
func validateSpec(doc map[string]interface{}) []string {
	var problems []string
	report := func(format string, args ...interface{}) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	// --- Document level -----------------------------------------------------
	version, _ := doc["openapi"].(string)
	if !regexp.MustCompile(`^3\.0\.\d+$`).MatchString(version) {
		report("openapi = %q, want a 3.0.x version string", version)
	}

	info, ok := doc["info"].(map[string]interface{})
	if !ok {
		report("missing required `info` object")
	} else {
		for _, f := range []string{"title", "version"} {
			if s, _ := info[f].(string); strings.TrimSpace(s) == "" {
				report("info.%s is required and must be non-empty", f)
			}
		}
	}

	// Every server URL must be absolute: a relative one makes the document
	// useless for generating a working client.
	servers, _ := doc["servers"].([]interface{})
	if len(servers) == 0 {
		report("no servers declared; a client cannot be generated without a base URL")
	}
	for i, raw := range servers {
		s, ok := raw.(map[string]interface{})
		if !ok {
			report("servers[%d] is not an object", i)
			continue
		}
		u, _ := s["url"].(string)
		if !strings.HasPrefix(u, "https://") {
			report("servers[%d].url = %q, want an absolute https:// URL", i, u)
		}
	}

	paths, ok := doc["paths"].(map[string]interface{})
	if !ok || len(paths) == 0 {
		report("spec declares no paths")
		return problems
	}

	// --- Every $ref must resolve -------------------------------------------
	var walk func(v interface{})
	var refs []string
	walk = func(v interface{}) {
		switch n := v.(type) {
		case map[string]interface{}:
			for k, child := range n {
				if k == "$ref" {
					if s, ok := child.(string); ok {
						refs = append(refs, s)
					}
					continue
				}
				walk(child)
			}
		case []interface{}:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(doc)
	for _, ref := range refs {
		if _, err := resolveRef(doc, ref); err != nil {
			report("%v", err)
		}
	}

	// --- Paths --------------------------------------------------------------
	for p := range paths {
		if !strings.HasPrefix(p, "/") {
			report("path %q must begin with /", p)
		}
	}

	// --- Parameters ---------------------------------------------------------
	checkParam := func(where string, raw interface{}) {
		p, ok := raw.(map[string]interface{})
		if !ok {
			report("%s: parameter is not an object", where)
			return
		}
		if _, isRef := p["$ref"]; isRef {
			return // resolution already checked above
		}
		name, _ := p["name"].(string)
		if strings.TrimSpace(name) == "" {
			report("%s: parameter is missing the required `name`", where)
		}
		in, _ := p["in"].(string)
		switch in {
		case "query", "header", "path", "cookie":
		default:
			report("%s: parameter %q has in=%q, want query, header, path or cookie", where, name, in)
		}
		// A path parameter MUST be declared required.
		if in == "path" {
			if req, _ := p["required"].(bool); !req {
				report("%s: path parameter %q must be declared required:true", where, name)
			}
		}
		if _, hasSchema := p["schema"]; !hasSchema {
			report("%s: parameter %q has no schema", where, name)
		}
	}

	// --- Operations ---------------------------------------------------------
	seenOperationIDs := map[string]string{}
	for p, raw := range paths {
		item, ok := raw.(map[string]interface{})
		if !ok {
			report("path %q is not an object", p)
			continue
		}

		if params, ok := item["parameters"].([]interface{}); ok {
			for i, prm := range params {
				checkParam(fmt.Sprintf("path %s parameters[%d]", p, i), prm)
			}
		}

		for key, val := range item {
			if !httpMethods[key] {
				if !pathItemFields[key] && !strings.HasPrefix(key, "x-") {
					report("path %q has unrecognised key %q: not an HTTP method, a Path Item field, or an x- extension", p, key)
				}
				continue
			}
			op, ok := val.(map[string]interface{})
			if !ok {
				report("%s %s is not an object", strings.ToUpper(key), p)
				continue
			}
			where := fmt.Sprintf("%s %s", strings.ToUpper(key), p)

			if params, ok := op["parameters"].([]interface{}); ok {
				for i, prm := range params {
					checkParam(fmt.Sprintf("%s parameters[%d]", where, i), prm)
				}
			}

			// responses is the only REQUIRED field on an operation.
			responses, ok := op["responses"].(map[string]interface{})
			if !ok || len(responses) == 0 {
				report("%s: `responses` is required and must declare at least one response", where)
				continue
			}
			for code, rawResp := range responses {
				if code != "default" {
					n, err := strconv.Atoi(code)
					if err != nil || n < 100 || n > 599 {
						report("%s: response key %q is neither an HTTP status code nor \"default\"", where, code)
					}
				}
				r, ok := rawResp.(map[string]interface{})
				if !ok {
					report("%s: response %s is not an object", where, code)
					continue
				}
				// A $ref carries its description on the component it points at.
				if ref, isRef := r["$ref"].(string); isRef {
					target, err := resolveRef(doc, ref)
					if err != nil {
						continue // already reported
					}
					tm, ok := target.(map[string]interface{})
					if !ok {
						report("%s: response %s $ref %q does not point at an object", where, code, ref)
						continue
					}
					if d, _ := tm["description"].(string); strings.TrimSpace(d) == "" {
						report("%s: response %s references %q, which is missing the required `description`", where, code, ref)
					}
					continue
				}
				// description is REQUIRED on a Response Object.
				if d, _ := r["description"].(string); strings.TrimSpace(d) == "" {
					report("%s: response %s is missing the required `description`", where, code)
				}
			}

			// operationId, when present, must be unique across the document.
			if id, _ := op["operationId"].(string); id != "" {
				if prev, dup := seenOperationIDs[id]; dup {
					report("%s: operationId %q already used by %s", where, id, prev)
				}
				seenOperationIDs[id] = where
			}

			// A request body on a method that cannot carry one is a modelling bug.
			if _, has := op["requestBody"]; has && (key == "get" || key == "head") {
				report("%s: declares a requestBody, which %s cannot carry", where, strings.ToUpper(key))
			}
		}
	}

	// Reusable parameters under components.
	if comps, ok := doc["components"].(map[string]interface{}); ok {
		if cp, ok := comps["parameters"].(map[string]interface{}); ok {
			for name, prm := range cp {
				checkParam("components.parameters."+name, prm)
			}
		}
	}

	// Every {placeholder} in a path template must have a matching declared
	// parameter, or a generated client will emit an unfillable URL.
	placeholder := regexp.MustCompile(`\{([^}]+)\}`)
	for p, raw := range paths {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		declared := map[string]bool{}
		collect := func(params []interface{}) {
			for _, prm := range params {
				m, ok := prm.(map[string]interface{})
				if !ok {
					continue
				}
				if n, _ := m["name"].(string); n != "" {
					declared[n] = true
				}
			}
		}
		if params, ok := item["parameters"].([]interface{}); ok {
			collect(params)
		}
		for key, val := range item {
			if !httpMethods[key] {
				continue
			}
			if op, ok := val.(map[string]interface{}); ok {
				if params, ok := op["parameters"].([]interface{}); ok {
					collect(params)
				}
			}
		}
		for _, m := range placeholder.FindAllStringSubmatch(p, -1) {
			if !declared[m[1]] {
				report("path %q uses {%s} but declares no parameter with that name", p, m[1])
			}
		}
	}

	return problems
}

func TestObservedSpecIsValidJSON(t *testing.T) {
	doc := loadSpec(t)
	if doc["openapi"] == nil {
		t.Error("missing openapi version field")
	}
	paths, ok := doc["paths"].(map[string]interface{})
	if !ok || len(paths) == 0 {
		t.Fatal("spec declares no paths")
	}
}

// The document itself must satisfy every structural rule.
func TestObservedSpecIsStructurallyValid(t *testing.T) {
	doc := loadSpec(t)
	for _, p := range validateSpec(doc) {
		t.Error(p)
	}
}

// Guard the document's whole reason for existing: claims must say whether they
// were observed live or merely inferred from F5's published specification.
func TestObservedSpecAnnotatesVerification(t *testing.T) {
	raw, err := os.ReadFile(observedSpec)
	if err != nil {
		t.Fatalf("cannot read %s: %v", observedSpec, err)
	}
	if !strings.Contains(string(raw), "x-verified") {
		t.Fatal("spec carries no x-verified annotations; unverified claims must be labelled as such")
	}

	doc := loadSpec(t)
	paths, _ := doc["paths"].(map[string]interface{})
	var missing []string
	for p, raw := range paths {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		for key, val := range item {
			if !httpMethods[key] {
				continue
			}
			op, ok := val.(map[string]interface{})
			if !ok {
				continue
			}
			if s, _ := op["x-verified"].(string); strings.TrimSpace(s) == "" {
				missing = append(missing, fmt.Sprintf("%s %s", strings.ToUpper(key), p))
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("operations with no x-verified annotation: %s\n"+
			"Every operation must state when it was last confirmed live, or say UNVERIFIED.",
			strings.Join(missing, ", "))
	}
}

// Facts this document exists to record. Each was confirmed against the live
// API and each contradicts F5's published specification, so if one is ever
// quietly edited away, that is a regression worth failing on.
func TestObservedSpecRecordsVerifiedFindings(t *testing.T) {
	doc := loadSpec(t)
	paths := doc["paths"].(map[string]interface{})

	// The doubled v1 on Downloads. Dropping the leading segment 404s.
	if _, ok := paths["/v1/downloads-management/v1/downloads/metadata"]; !ok {
		t.Error("Downloads metadata path must keep its leading /v1: without it the gateway returns 404")
	}

	// serialNumber is required on create despite the published spec calling it
	// optional. This is the single thing blocking case creation.
	comps := doc["components"].(map[string]interface{})
	schemas := comps["schemas"].(map[string]interface{})
	create := schemas["CaseCreateRequest"].(map[string]interface{})
	req, _ := create["required"].([]interface{})
	var hasSerial bool
	for _, f := range req {
		if f == "serialNumber" {
			hasSerial = true
		}
	}
	if !hasSerial {
		t.Error("CaseCreateRequest must list serialNumber as required: the live API rejects creates without it")
	}
}

// The validator must actually detect the problems it claims to. Each mutation
// below breaks one rule; the validator is expected to object to every one.
// Without this, a validator bug would look exactly like a clean document.
func TestValidatorDetectsBrokenSpecs(t *testing.T) {
	raw, err := os.ReadFile(observedSpec)
	if err != nil {
		t.Fatal(err)
	}

	// Sanity check: the unmutated document must be clean, or the negative
	// results below would be meaningless.
	var pristine map[string]interface{}
	if err := json.Unmarshal(raw, &pristine); err != nil {
		t.Fatal(err)
	}
	if p := validateSpec(pristine); len(p) != 0 {
		t.Fatalf("the real spec does not validate, so these negative tests prove nothing: %v", p)
	}

	casePath := "/case-management/v1/cases"

	mutations := map[string]func(map[string]interface{}){
		"dangling $ref": func(d map[string]interface{}) {
			g := d["paths"].(map[string]interface{})[casePath].(map[string]interface{})["get"].(map[string]interface{})
			g["parameters"] = []interface{}{map[string]interface{}{"$ref": "#/components/parameters/DoesNotExist"}}
		},
		"response missing description": func(d map[string]interface{}) {
			g := d["paths"].(map[string]interface{})[casePath].(map[string]interface{})["get"].(map[string]interface{})
			g["responses"].(map[string]interface{})["200"] = map[string]interface{}{}
		},
		"operation with no responses": func(d map[string]interface{}) {
			g := d["paths"].(map[string]interface{})[casePath].(map[string]interface{})["get"].(map[string]interface{})
			delete(g, "responses")
		},
		"nonsense response code": func(d map[string]interface{}) {
			g := d["paths"].(map[string]interface{})[casePath].(map[string]interface{})["get"].(map[string]interface{})
			g["responses"].(map[string]interface{})["banana"] = map[string]interface{}{"description": "ok"}
		},
		"GET with requestBody": func(d map[string]interface{}) {
			g := d["paths"].(map[string]interface{})[casePath].(map[string]interface{})["get"].(map[string]interface{})
			g["requestBody"] = map[string]interface{}{"content": map[string]interface{}{}}
		},
		"undeclared path placeholder": func(d map[string]interface{}) {
			d["paths"].(map[string]interface{})["/thing/{missingParam}"] = map[string]interface{}{
				"get": map[string]interface{}{
					"responses": map[string]interface{}{"200": map[string]interface{}{"description": "ok"}},
				},
			}
		},
		"path parameter not required": func(d map[string]interface{}) {
			d["paths"].(map[string]interface{})["/thing/{id}"] = map[string]interface{}{
				"parameters": []interface{}{map[string]interface{}{
					"name": "id", "in": "path", "required": false,
					"schema": map[string]interface{}{"type": "string"},
				}},
				"get": map[string]interface{}{
					"responses": map[string]interface{}{"200": map[string]interface{}{"description": "ok"}},
				},
			}
		},
		"parameter with bad `in`": func(d map[string]interface{}) {
			g := d["paths"].(map[string]interface{})[casePath].(map[string]interface{})["get"].(map[string]interface{})
			g["parameters"] = []interface{}{map[string]interface{}{
				"name": "x", "in": "body", "schema": map[string]interface{}{"type": "string"},
			}}
		},
		"relative server url": func(d map[string]interface{}) {
			d["servers"] = []interface{}{map[string]interface{}{"url": "/api"}}
		},
		"missing info.title": func(d map[string]interface{}) {
			delete(d["info"].(map[string]interface{}), "title")
		},
		"wrong openapi version": func(d map[string]interface{}) {
			d["openapi"] = "2.0"
		},
		"typo'd key on a path item": func(d map[string]interface{}) {
			d["paths"].(map[string]interface{})[casePath].(map[string]interface{})["gett"] = map[string]interface{}{}
		},
		"duplicate operationId": func(d map[string]interface{}) {
			paths := d["paths"].(map[string]interface{})
			paths[casePath].(map[string]interface{})["get"].(map[string]interface{})["operationId"] = "dupe"
			paths[casePath].(map[string]interface{})["post"].(map[string]interface{})["operationId"] = "dupe"
		},
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var doc map[string]interface{}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			mutate(doc)
			if problems := validateSpec(doc); len(problems) == 0 {
				t.Errorf("mutation %q was not detected: the validator would pass a broken document", name)
			}
		})
	}
}
