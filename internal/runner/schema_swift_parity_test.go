// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

//go:build darwin

package runner

// Cross-language parity for the guided-output subset. The shipped helper
// parser (native/SchemaSubset.swift) and this package's parser are compiled and
// run side by side over one case table; a divergence in acceptance, JSON
// pointer or rejection reason fails here. The Swift build is opt-in because it
// needs the macOS 27 SDK, so this test skips unless HOLLIS_SCHEMA_PARITY=1.
//
// Run it with:
//   HOLLIS_SCHEMA_PARITY=1 go test ./internal/runner -run TestSchemaSwiftParity -v

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/subproc"
)

const swiftSubsetHarness = `import Foundation

struct Case: Decodable {
    let schema: String
    let value: String?
    let request: String?
    let request_want: Verdict?
}

struct Verdict: Codable, Equatable {
    let ok: Bool
    let pointer: String
    let reason: String
}

let input = FileHandle.standardInput.readDataToEndOfFile()
let cases: [Case]
do { cases = try JSONDecoder().decode([Case].self, from: input) }
catch { print("[]"); exit(0) }

var verdicts: [Verdict] = []
for item in cases {
    do {
        // A request case exercises the helper's own entry path: the strict scan
        // over the whole request and the extraction of the schema member, which
        // must return exactly the bytes carried in "schema".
        if let want = item.request_want {
            let actual: Verdict
            do {
                _ = try StrictJSON.schemaMember(of: Data((item.request ?? "").utf8))
                actual = Verdict(ok: true, pointer: "", reason: "")
            } catch let failure as SchemaSubset.Failure {
                actual = Verdict(ok: false, pointer: failure.pointer, reason: failure.reason)
            } catch {
                actual = Verdict(ok: false, pointer: "", reason: "unknown")
            }
            guard actual == want else {
                verdicts.append(Verdict(ok: false, pointer: "", reason: "request verdict mismatch"))
                continue
            }
            verdicts.append(actual)
            continue
        }
        if let request = item.request {
            guard let extracted = try StrictJSON.schemaMember(of: Data(request.utf8)),
                  extracted == Data(item.schema.utf8) else {
                verdicts.append(Verdict(ok: false, pointer: "", reason: "schema member mismatch"))
                continue
            }
        }
        // The real helper path: raw schema bytes go through the strict scan and
        // Foundation's decoder before the subset parser sees a value.
        let decoded = try SchemaSubset.decodeSchema(Data(item.schema.utf8))
        let node = try SchemaSubset.parse(decoded)
        if let text = item.value {
            let payload = try SchemaSubset.decodePayload(Data(text.utf8))
            if let bad = SchemaSubset.validate(payload, against: node) {
                verdicts.append(Verdict(ok: false, pointer: bad, reason: "returned payload does not match the requested schema"))
            } else {
                verdicts.append(Verdict(ok: true, pointer: "", reason: ""))
            }
        } else {
            verdicts.append(Verdict(ok: true, pointer: "", reason: ""))
        }
    } catch let failure as SchemaSubset.Failure {
        verdicts.append(Verdict(ok: false, pointer: failure.pointer, reason: failure.reason))
    } catch {
        verdicts.append(Verdict(ok: false, pointer: "", reason: "unknown"));
    }
}
let encoded = try JSONEncoder().encode(verdicts)
print(String(decoding: encoded, as: UTF8.self))
`

type swiftParityCase struct {
	Name string `json:"name"`
	// Schema and Value carry raw document bytes, not pre-decoded values: the
	// helper's own decode is part of what is compared, so a case that is not
	// valid JSON, carries a BOM or repeats a key must reach the scanner intact.
	Schema string `json:"schema"`
	Value  string `json:"value,omitempty"`
	// Request, when set, is a whole helper request embedding Schema. The
	// harness must extract exactly Schema from it, which is how the helper's
	// own request path is covered.
	Request string `json:"request,omitempty"`
	// RequestWant, when set, is the exact verdict the whole request must
	// produce ("ok", or a pointer and reason). It covers the request envelope,
	// which has no Go-side equivalent to compare against, so the expectation is
	// written out here.
	RequestWant *schemaVerdict `json:"request_want,omitempty"`
}

type schemaVerdict struct {
	OK      bool   `json:"ok"`
	Pointer string `json:"pointer"`
	Reason  string `json:"reason"`
}

// goSchemaVerdict runs this package's parser and payload validator.
func goSchemaVerdict(t *testing.T, tc swiftParityCase) schemaVerdict {
	t.Helper()
	if tc.RequestWant != nil {
		return *tc.RequestWant
	}
	schema, err := ParseSchema([]byte(tc.Schema))
	if err == nil && tc.Value != "" {
		err = schema.Validate([]byte(tc.Value))
	}
	if err == nil {
		return schemaVerdict{OK: true}
	}
	var failure *SchemaError
	if !errors.As(err, &failure) {
		t.Fatalf("%s: unexpected error type %v", tc.Name, err)
	}
	return schemaVerdict{Pointer: failure.Pointer, Reason: failure.Reason}
}

// swiftTool runs one build-time command through the shared lifecycle so the
// compiler is bounded, group-killed on expiry, and its output capped like every
// other child this package spawns. It is not a model call.
func swiftTool(t *testing.T, name string, args ...string) (string, string, bool) {
	t.Helper()
	res, err := subproc.Run(t.Context(), subproc.Spec{
		Name:      name,
		Args:      args,
		Timeout:   10 * time.Minute,
		StdoutCap: 1 << 20,
		StderrCap: 1 << 20,
	})
	if err != nil || res.WaitErr != nil {
		return string(res.Stdout), string(res.Stderr), false
	}
	return string(res.Stdout), string(res.Stderr), true
}

func TestSchemaSwiftParity(t *testing.T) {
	if os.Getenv("HOLLIS_SCHEMA_PARITY") != "1" {
		t.Skip("set HOLLIS_SCHEMA_PARITY=1 to compile the shipped Swift subset parser and compare verdicts")
	}
	if runtime.GOARCH != "arm64" {
		t.Skip("the shipped helper target is arm64 only")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	subset := filepath.Join(root, "native", "SchemaSubset.swift")
	if _, err := os.Stat(subset); err != nil {
		t.Fatalf("shipped subset parser unavailable: %v", err)
	}
	sdkPath, _, ok := swiftTool(t, "xcrun", "--sdk", "macosx", "--show-sdk-path")
	if !ok {
		t.Skip("no macOS SDK available for the shipped subset parser")
	}
	sdkPath = strings.TrimSpace(sdkPath)
	version, _, ok := swiftTool(t, "xcrun", "--sdk", "macosx", "--show-sdk-version")
	if !ok || !strings.HasPrefix(strings.TrimSpace(version), "27") {
		t.Skipf("macOS SDK 27 is required for the shipped subset parser (found %q)", strings.TrimSpace(version))
	}

	dir := t.TempDir()
	harness := filepath.Join(dir, "main.swift")
	if err := os.WriteFile(harness, []byte(swiftSubsetHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "schema-parity")
	if _, log, ok := swiftTool(t, "xcrun", "swiftc", "-sdk", sdkPath, "-target", "arm64-apple-macosx27.0",
		subset, harness, "-o", binary); !ok {
		t.Fatalf("swift harness build failed:\n%s", log)
	}

	cases := swiftParityCases()
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	res, err := subproc.Run(t.Context(), subproc.Spec{
		Name:      binary,
		Stdin:     payload,
		Timeout:   2 * time.Minute,
		StdoutCap: 1 << 20,
		StderrCap: 1 << 20,
	})
	if err != nil || res.WaitErr != nil {
		t.Fatalf("swift harness run failed: %v %v\n%s", err, res.WaitErr, res.Stderr)
	}
	var swift []schemaVerdict
	if err := json.Unmarshal(res.Stdout, &swift); err != nil {
		t.Fatalf("swift verdicts: %v (%q)", err, res.Stdout)
	}
	if len(swift) != len(cases) {
		t.Fatalf("swift reported %d verdicts for %d cases", len(swift), len(cases))
	}
	for index, tc := range cases {
		want := goSchemaVerdict(t, tc)
		if swift[index] != want {
			t.Fatalf("%s: swift %+v, go %+v", tc.Name, swift[index], want)
		}
	}
	t.Logf("%d cases agree between internal/runner and native/SchemaSubset.swift", len(cases))
}

// swiftParityCases is one table of accepted, rejected and non-conforming
// inputs. Every case has a single fault so both parsers report the same one.
// Cases written before the widenings stay as they were: "missing
// additionalProperties" and "unknown keyword" are now acceptances on both
// sides, and the table still requires the two sides to agree on them.
func swiftParityCases() []swiftParityCase {
	object := func(properties, required string) string {
		schema := `{"type":"object","additionalProperties":false`
		if properties != "" {
			schema += `,"properties":` + properties
		}
		if required != "" {
			schema += `,"required":` + required
		}
		return schema + `}`
	}
	typed := func(property string) string {
		return object(`{"p":`+property+`}`, `["p"]`)
	}
	nested := func(levels int) string {
		schema := `{"type":"string"}`
		for index := 0; index < levels; index++ {
			schema = object(`{"p":`+schema+`}`, "")
		}
		return schema
	}
	arrays := func(count int) string {
		schema := `{"type":"string"}`
		for index := 0; index < count; index++ {
			schema = `{"type":"array","items":` + schema + `}`
		}
		return typed(schema)
	}
	now := func(name, schema string) swiftParityCase {
		return swiftParityCase{Name: name, Schema: schema}
	}
	withValue := func(name, schema, value string) swiftParityCase {
		return swiftParityCase{Name: name, Schema: schema, Value: value}
	}
	// A whole request, as Go writes it, embedding the schema verbatim. The
	// prompt carries request-shaped text so a scanner that looked for the
	// literal bytes `"schema"` inside a string would extract the wrong slice.
	withRequest := func(name, schema, prompt string) swiftParityCase {
		request := `{"protocol":2,"operation":"schema","prompt":` + strconv.Quote(prompt) + `,"schema":` + schema + `}`
		return swiftParityCase{Name: name, Schema: schema, Request: request}
	}
	requestWant := func(name, request string, want schemaVerdict) swiftParityCase {
		return swiftParityCase{Name: name, Request: request, RequestWant: &want}
	}
	// The 64 KiB gate is measured on the normalized encoding, and Foundation
	// writes every solidus as \/. This schema carries 33 KB of solidus in the
	// source: escaped, it is over the gate; left bare it would be under it. The
	// case only agrees when both sides escape alike.
	solidus := now("size gate counts escaped solidus",
		typed(`{"type":"string","enum":["`+strings.Repeat("/", 33000)+`"]}`))
	return []swiftParityCase{
		now("empty object", object("", "")),
		now("string", typed(`{"type":"string"}`)),
		now("string enum", typed(`{"type":"string","enum":["a","b"]}`)),
		now("integer bounds", typed(`{"type":"integer","minimum":0,"maximum":10}`)),
		now("number bounds", typed(`{"type":"number","minimum":-1.5,"maximum":1.5}`)),
		now("boolean", typed(`{"type":"boolean"}`)),
		now("array", typed(`{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":2}`)),
		now("array of objects", typed(`{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"a":{"type":"string"}}}}`)),
		now("depth 3", nested(3)),
		now("depth 4", nested(4)),
		now("missing additionalProperties", `{"type":"object"}`),
		now("additionalProperties true", `{"type":"object","additionalProperties":true}`),
		now("root array", `{"type":"array","items":{"type":"string"}}`),
		now("unknown keyword", typed(`{"type":"string","pattern":"x"}`)),
		now("reference", `{"type":"object","additionalProperties":false,"$ref":"#/x"}`),
		now("integer enum", typed(`{"type":"integer","enum":[1,2]}`)),
		now("string enum with numbers", typed(`{"type":"string","enum":[1,2]}`)),
		now("empty enum", typed(`{"type":"string","enum":[]}`)),
		now("duplicate enum", typed(`{"type":"string","enum":["a","a"]}`)),
		now("items missing", typed(`{"type":"array"}`)),
		now("minItems 100", typed(`{"type":"array","items":{"type":"string"},"minItems":100}`)),
		now("maxItems 100", typed(`{"type":"array","items":{"type":"string"},"maxItems":100}`)),
		now("minItems 101", typed(`{"type":"array","items":{"type":"string"},"minItems":101}`)),
		now("fractional minItems", typed(`{"type":"array","items":{"type":"string"},"minItems":1.5}`)),
		now("contradictory item bounds", typed(`{"type":"array","items":{"type":"string"},"minItems":3,"maxItems":2}`)),
		now("fractional integer bound", typed(`{"type":"integer","minimum":1.5}`)),
		now("integer bound too large", typed(`{"type":"integer","maximum":1e300}`)),
		now("contradictory integer bounds", typed(`{"type":"integer","minimum":5,"maximum":1}`)),
		now("required not array", `{"type":"object","additionalProperties":false,"required":"p"}`),
		now("required undeclared", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":["q"]}`),
		now("required duplicate", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":["p","p"]}`),
		now("properties not object", `{"type":"object","additionalProperties":false,"properties":[]}`),
		now("empty property name", `{"type":"object","additionalProperties":false,"properties":{"":{"type":"string"}}}`),
		now("escaped pointer", `{"type":"object","additionalProperties":false,"properties":{"a/b~c":{"type":"string","pattern":"x"}}}`),
		now("missing type", typed(`{"properties":{}}`)),
		now("unsupported type", typed(`{"type":"null"}`)),
		// Authoring names and enum choices are printable ASCII on both sides.
		// The precomposed and decomposed pairs are canonically equivalent, so
		// a byte-comparing consumer and a canonical-equivalence helper would
		// otherwise treat the same schema differently.
		now("precomposed property name", `{"type":"object","additionalProperties":false,"properties":{"\u00e9":{"type":"string"}}}`),
		now("decomposed property name", `{"type":"object","additionalProperties":false,"properties":{"e\u0301":{"type":"string"}}}`),
		now("precomposed required name", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":["\u00e9"]}`),
		now("decomposed required name", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":["e\u0301"]}`),
		now("precomposed enum choice", typed(`{"type":"string","enum":["caf\u00e9"]}`)),
		now("decomposed enum choice", typed(`{"type":"string","enum":["cafe\u0301"]}`)),
		// Object depth stops objects at three levels; this bound also covers
		// array chains, whose items can be another array.
		now("array nesting 14", arrays(14)),
		now("array nesting 15", arrays(15)),
		now("array nesting 20", arrays(20)),
		solidus,

		// Foundation's decoder resolves a repeated key by keeping the first
		// value and accepts a BOM and trailing commas; Go rejects all three.
		// These cases reach the helper as raw bytes, so the strict scan is what
		// makes the two sides agree.
		now("duplicate root type", `{"type":"object","additionalProperties":false,"type":"array"}`),
		now("duplicate additionalProperties", `{"type":"object","additionalProperties":false,"additionalProperties":false}`),
		now("duplicate property name", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"},"p":{"type":"integer"}}}`),
		now("duplicate required member", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":["p"],"required":["p"]}`),
		now("duplicate nested type", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"object","additionalProperties":false,"properties":{"q":{"type":"string","type":"integer"}}}}}`),
		now("duplicate items", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"array","items":{"type":"string"},"items":{"type":"string"}}}}`),
		now("duplicate key via escaped solidus", `{"type":"object","additionalProperties":false,"properties":{"a/b":{"type":"string"},"a\/b":{"type":"string"}}}`),
		now("duplicate key via unicode escape", `{"type":"object","additionalProperties":false,"properties":{"a":{"type":"string"},"\u0061":{"type":"string"}}}`),
		now("byte order mark", "\ufeff"+`{"type":"object","additionalProperties":false}`),
		now("trailing comma in object", `{"type":"object","additionalProperties":false,}`),
		now("trailing comma in enum array", typed(`{"type":"string","enum":["a",]}`)),
		// The same documents embedded in a helper request, which is how the
		// helper really receives them. Extraction must return exactly the
		// schema bytes, and the prompt text must not confuse it.
		withRequest("request with schema", typed(`{"type":"string"}`), `say "schema": {"type":"object"} please`),
		withRequest("request with duplicate schema key", `{"type":"object","additionalProperties":false,"type":"array"}`, "hi"),
		withRequest("request with escaped duplicate", `{"type":"object","additionalProperties":false,"properties":{"a":{"type":"string"},"\u0061":{"type":"string"}}}`, "hi"),
		// The envelope is scanned too: a repeated `schema` member would let
		// Foundation's keep-the-first decoder read a different value than the
		// one the helper scanned, and a repeated any other member is a request
		// Go never writes.
		requestWant("request with repeated schema member",
			`{"protocol":2,"operation":"schema","prompt":"hi","schema":{"type":"object","additionalProperties":false},"schema":{"type":"object","additionalProperties":false,"type":"array"}}`,
			schemaVerdict{Pointer: "/schema", Reason: "request must not repeat an object key"}),
		requestWant("request with repeated protocol member",
			`{"protocol":2,"protocol":2,"operation":"schema","prompt":"hi","schema":{"type":"object","additionalProperties":false}}`,
			schemaVerdict{Pointer: "/protocol", Reason: "request must not repeat an object key"}),

		// Payload cases carry the value inside the declared property. A bare
		// scalar against an object root agrees trivially, so it would not
		// compare any of the checks these cases are named for.
		withValue("payload conforming", typed(`{"type":"integer","minimum":0,"maximum":10}`), `{"p":3}`),
		withValue("payload wrong type", typed(`{"type":"integer"}`), `{"p":"3"}`),
		withValue("payload fractional", typed(`{"type":"integer"}`), `{"p":1.5}`),
		withValue("payload above maximum", typed(`{"type":"integer","maximum":10}`), `{"p":11}`),
		withValue("payload below minimum", typed(`{"type":"integer","minimum":0}`), `{"p":-1}`),
		withValue("payload number above maximum", typed(`{"type":"number","maximum":1.5}`), `{"p":1.6}`),
		withValue("payload enum mismatch", typed(`{"type":"string","enum":["a"]}`), `{"p":"b"}`),
		withValue("payload array below minItems", typed(`{"type":"array","items":{"type":"string"},"minItems":1}`), `{"p":[]}`),
		withValue("payload array above maxItems", typed(`{"type":"array","items":{"type":"string"},"maxItems":1}`), `{"p":["a","b"]}`),
		withValue("payload array item fault", typed(`{"type":"array","items":{"type":"integer"}}`), `{"p":[1,"b"]}`),
		withValue("payload missing required", typed(`{"type":"string"}`), `{}`),
		withValue("payload extra property", typed(`{"type":"string"}`), `{"p":"a","q":1}`),
		withValue("payload two extra properties", typed(`{"type":"string"}`), `{"p":"a","b":1,"a":2}`),
		withValue("payload nested fault", object(`{"p":`+typed(`{"type":"string"}`)+`}`, ""), `{"p":{"p":1}}`),
		withValue("payload root mismatch", object("", ""), `[1]`),
		withValue("payload duplicate key", typed(`{"type":"string"}`), `{"p":"a","p":"b"}`),
		withValue("payload trailing comma", typed(`{"type":"string"}`), `{"p":"a",}`),

		// The widenings. Annotations are ignored at any node, string
		// constraints are accepted and not enforced, an absent
		// additionalProperties means false, and a nullable union collapses to
		// its one non-null branch as an optional property.
		now("annotations on root and property", `{"$schema":"s","$id":"i","$comment":"c","title":"T","description":"D",`+
			`"type":"object","additionalProperties":false,"properties":{"p":{"type":"integer","title":"P","description":"d",`+
			`"default":1,"examples":[1],"deprecated":true,"readOnly":true,"writeOnly":false,"x-mcp-header":"h"}}}`),
		now("annotations on array items", typed(`{"type":"array","default":[],"items":{"type":"string","x-k":{"a":[1]}}}`)),
		now("string constraints", typed(`{"type":"string","format":"uri","minLength":1,"maxLength":9,"pattern":"^a",`+
			`"contentEncoding":"base64","contentMediaType":"text/plain"}`)),
		now("additionalProperties absent at the root", `{"type":"object","properties":{"p":{"type":"string"}}}`),
		now("additionalProperties absent when nested", typed(`{"type":"object","properties":{"q":{"type":"string"}}}`)),
		now("nullable anyOf", `{"type":"object","properties":{"p":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null}}}`),
		now("nullable oneOf, null first", `{"type":"object","properties":{"p":{"oneOf":[{"type":"null"},{"type":"integer","maximum":3}]}}}`),
		now("nullable anyOf named required", `{"type":"object","properties":{"p":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["p"]}`),
		withValue("payload ignores string constraints", typed(`{"type":"string","minLength":5,"pattern":"^z"}`), `{"p":"a"}`),
		withValue("payload extra property without additionalProperties", `{"type":"object","properties":{"p":{"type":"string"}}}`, `{"p":"a","q":1}`),
		withValue("payload nested extra property without additionalProperties", typed(`{"type":"object","properties":{"q":{"type":"string"}}}`), `{"p":{"q":"a","r":1}}`),
		withValue("payload null for a nullable property", `{"type":"object","properties":{"p":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["p"]}`, `{"p":null}`),
		withValue("payload nullable property omitted", `{"type":"object","properties":{"p":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["p"]}`, `{}`),
		withValue("payload nullable branch enum mismatch", `{"type":"object","properties":{"p":{"anyOf":[{"type":"string","enum":["a"]},{"type":"null"}]}}}`, `{"p":"b"}`),
		withValue("payload nullable branch bound", `{"type":"object","properties":{"p":{"oneOf":[{"type":"null"},{"type":"integer","maximum":3}]}}}`, `{"p":4}`),
		withValue("payload null for an ordinary property", typed(`{"type":"string"}`), `{"p":null}`),
		withValue("payload default is not applied", `{"type":"object","properties":{"p":{"type":"string","default":"a"}},"required":["p"]}`, `{}`),
		// Rejections that must not become acceptances.
		now("null branch description not a string", `{"type":"object","properties":{"p":{"anyOf":[{"type":"string"},{"type":"null","description":{"no":1}}]}}}`),
		now("null branch title not a string", `{"type":"object","properties":{"p":{"anyOf":[{"type":"string"},{"type":"null","title":["x"]}]}}}`),
		now("additionalProperties true when nested", typed(`{"type":"object","additionalProperties":true}`)),
		now("additionalProperties schema", `{"type":"object","additionalProperties":{"type":"string"}}`),
		now("additionalProperties zero", `{"type":"object","additionalProperties":0}`),
		now("multi-type union", typed(`{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"}}]}`)),
		now("multi-type union with null", typed(`{"anyOf":[{"type":"string"},{"type":"integer"},{"type":"null"}]}`)),
		now("null-only union", typed(`{"anyOf":[{"type":"null"}]}`)),
		now("single-branch union", typed(`{"oneOf":[{"type":"string"}]}`)),
		now("empty union", typed(`{"anyOf":[]}`)),
		now("union not an array", typed(`{"anyOf":{"type":"string"}}`)),
		now("anyOf beside oneOf", typed(`{"anyOf":[{"type":"string"},{"type":"null"}],"oneOf":[]}`)),
		now("union beside a constraint", typed(`{"anyOf":[{"type":"string"},{"type":"null"}],"minLength":1}`)),
		now("union beside a type", typed(`{"type":"string","anyOf":[{"type":"string"},{"type":"null"}]}`)),
		now("null branch with a keyword", typed(`{"anyOf":[{"type":"string"},{"type":"null","enum":["a"]}]}`)),
		now("non-object union branch", typed(`{"anyOf":["x",{"type":"null"}]}`)),
		now("union at the root", `{"type":"object","anyOf":[{"type":"object"},{"type":"null"}]}`),
		now("union in array items", typed(`{"type":"array","items":{"anyOf":[{"type":"string"},{"type":"null"}]}}`)),
		now("dropped integer enum in a nullable branch", typed(`{"anyOf":[{"type":"integer","enum":[1]},{"type":"null"}]}`)),
		now("integer enum beside annotations", typed(`{"type":"integer","description":"d","enum":[1,2]}`)),
		now("nullable branch past depth 3", `{"type":"object","properties":{"p":{"type":"object","properties":{"p":{"anyOf":[`+
			`{"type":"object","properties":{"p":{"type":"object","properties":{"p":{"type":"string"}}}}},{"type":"null"}]}}}}}`),
		now("nullable branch past nesting 16", typed(`{"anyOf":[`+strings.Repeat(`{"type":"array","items":`, 14)+`{"type":"string"}`+strings.Repeat("}", 14)+`,{"type":"null"}]}`)),
		now("string constraint on an integer", typed(`{"type":"integer","maxLength":1}`)),
		now("const", typed(`{"type":"string","const":"a"}`)),
		now("type list", typed(`{"type":["string","null"]}`)),
		now("non-string description", typed(`{"type":"string","description":1}`)),
		now("non-string title in a union", typed(`{"anyOf":[{"type":"string"},{"type":"null"}],"title":2}`)),
		now("non-string pattern", typed(`{"type":"string","pattern":1}`)),
		now("fractional minLength", typed(`{"type":"string","minLength":1.5}`)),
		now("boolean maxLength", typed(`{"type":"string","maxLength":true}`)),
		now("negative maxLength", typed(`{"type":"string","maxLength":-1}`)),
		now("annotation then unsupported keyword", typed(`{"type":"string","$comment":"c","const":"a","default":"b"}`)),
	}
}
