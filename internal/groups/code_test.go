package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/muthuishere/clicodemode/codemode"

	"github.com/muthuishere/aos/internal/cli"
)

func codeTestEngine(t *testing.T) (*codemode.Engine, *codemode.Registry) {
	t.Helper()
	c, _ := registeredCtx("linux", nil)
	eng := newCodeEngine(c)
	return eng, eng.Registry
}

func TestCodeOperationsCoverEveryBatchVerb(t *testing.T) {
	c, _ := registeredCtx("linux", nil)
	_, reg := codeTestEngine(t)
	for name := range batchVerbs(c.Registry) {
		if name == "do" {
			if _, ok := reg.Lookup(name); ok {
				t.Errorf("do must not be an operation")
			}
			continue
		}
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("no operation for verb %q", name)
		}
	}
	// every public command, by the rule batch uses
	for _, cmd := range c.Registry.Commands() {
		if cmd.Hidden || cmd.Run == nil || batchNoVerb[cmd.Group] || batchSystemGroups[cmd.Group] {
			continue
		}
		name := cmd.Group
		if cmd.Name != "" {
			name += "-" + strings.ReplaceAll(cmd.Name, " ", "-")
		}
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("no operation for %q", cmd.Route())
		}
	}
}

func TestCodeExcludesWhatBatchExcludes(t *testing.T) {
	_, reg := codeTestEngine(t)
	for _, op := range reg.Operations() {
		for g := range batchNoVerb {
			if op.Name == g || strings.HasPrefix(op.Name, g+"-") {
				t.Errorf("%s exposes excluded group %s", op.Name, g)
			}
		}
		for g := range batchSystemGroups {
			if op.Name == g || strings.HasPrefix(op.Name, g+"-") {
				t.Errorf("%s exposes system group %s", op.Name, g)
			}
		}
	}
	if !batchNoVerb["code"] {
		t.Error("code must be excluded from batch")
	}
}

// The argv `code` builds equals the argv a batch step builds.
func TestCodeArgvMatchesBatch(t *testing.T) {
	c, _ := registeredCtx("linux", nil)
	verbs := batchVerbs(c.Registry)
	cases := []struct {
		verb  string
		words []string
		flags map[string]any
		yaml  string
	}{
		{"window-list", nil, map[string]any{"json": true}, `[window-list: --json]`},
		{"window-list", nil, map[string]any{"json": false}, `[window-list: --json=false]`},
		{"window-focus", []string{"Notepad"}, nil, `[window-focus: Notepad]`},
		{"capture-screenshot", []string{"window"}, map[string]any{"app": "Calculator", "out": "a.png"}, `[capture-screenshot: window --app=Calculator --out=a.png]`},
		{"click", []string{"Calculator", "Seven"}, map[string]any{"double": true}, `[click: Calculator Seven --double]`},
		{"click", []string{"10", "20"}, nil, `[click: 10 20]`},
		{"type", []string{"Notepad", "hello", "world"}, nil, `[type: Notepad hello world]`},
		{"type", []string{"hi"}, nil, `[type: hi]`},
		{"key", []string{"Notepad", "ctrl+s"}, nil, `[key: Notepad ctrl+s]`},
		{"key", []string{"ctrl+s"}, nil, `[key: ctrl+s]`},
		{"screenshot", []string{"Calculator"}, map[string]any{"out": "c.png"}, `[screenshot: Calculator --out=c.png]`},
		{"screenshot", nil, nil, `[screenshot]`},
		{"say", []string{"hello", "there"}, nil, `[say: hello there]`},
	}
	for _, tc := range cases {
		steps, fs := checkBatch(c.Registry, []byte(tc.yaml))
		if len(steps) != 1 {
			t.Fatalf("%s: batch rejected %s: %v", tc.verb, tc.yaml, fs)
		}
		want := steps[0].Argv
		if tc.verb == "say" {
			want = append([]string{"say"}, steps[0].Say...)
		}
		in := map[string]any{}
		if tc.words != nil {
			a := []any{}
			for _, w := range tc.words {
				a = append(a, w)
			}
			in["args"] = a
		}
		for k, v := range tc.flags {
			in[k] = v
		}
		got, err := codeArgv(verbs[tc.verb], in)
		if err != nil {
			t.Fatalf("%s: %v", tc.verb, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: code %v, batch %v", tc.verb, got, want)
		}
	}
}

func TestCodeRunsAProgramAndNeverExecs(t *testing.T) {
	eng, _ := codeTestEngine(t)
	r := eng.Run(context.Background(), `
const [a, b] = await Promise.all([ops["wait"]({args: ["10"]}), ops["wait"]({args: ["1ms"]})])
;({ a: a === "", b: b === "" })`, "<t>")
	if !r.OK {
		t.Fatalf("run: %s %v", r.Error, r.Diagnostics)
	}
	var m map[string]any
	b, _ := json.Marshal(r.Value)
	_ = json.Unmarshal(b, &m)
	if m["a"] != true || m["b"] != true {
		t.Fatalf("got %v", m)
	}
	for _, src := range []string{
		`await exec(["echo","hi"])`,
		`await shell("echo hi")`,
		`await ops.batch({})`,
		`await ops.code({})`,
	} {
		if r := eng.Run(context.Background(), src, "<t>"); r.OK {
			t.Errorf("%s must not run", src)
		}
	}
}

func TestCodeRejectsUnknownFlags(t *testing.T) {
	eng, _ := codeTestEngine(t)
	r := eng.Run(context.Background(), `await ops["window-list"]({bogus: 1})`, "<t>")
	if r.OK {
		t.Fatal("unknown flag must be rejected before anything runs")
	}
}

// JSON-first: --json is appended when documented, the parsed value comes back,
// text comes back trimmed, and a failing command rejects with "exit N: ...".
func TestCodeResultsAreJSONFirst(t *testing.T) {
	c, _ := registeredCtx("linux", nil)
	var seen []string
	c.Registry.Add(
		&cli.Command{Group: "zz", Name: "js", Summary: "json", Args: "[--json]", Run: func(c *cli.Ctx, args []string) error {
			seen = args
			fmt.Fprint(c.Stdout, `[{"App":"a"},{"App":"b"}]`)
			return nil
		}},
		&cli.Command{Group: "zz", Name: "txt", Summary: "text", Args: "<x>", Run: func(c *cli.Ctx, args []string) error {
			fmt.Fprint(c.Stdout, "  hello \n")
			return nil
		}},
		&cli.Command{Group: "zz", Name: "bad", Summary: "fails", Args: "", Run: func(c *cli.Ctx, args []string) error {
			fmt.Fprint(c.Stderr, "boom happened")
			return &cli.ExitError{Code: 3}
		}},
	)
	eng := newCodeEngine(c)
	r := eng.Run(context.Background(), `
const j = await ops["zz-js"]({})
const t = await ops["zz-txt"]({args: ["x"]})
let err = ""
try { await ops["zz-bad"]({}) } catch (e) { err = e.message }
;({ n: j.length, app: j[1].App, t, err })`, "<t>")
	if !r.OK {
		t.Fatalf("run: %s %v", r.Error, r.Diagnostics)
	}
	b, _ := json.Marshal(r.Value)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["n"] != 2.0 || m["app"] != "b" || m["t"] != "hello" {
		t.Fatalf("got %v", m)
	}
	if e, _ := m["err"].(string); !strings.Contains(e, "exit 3: ") || !strings.Contains(e, "boom happened") {
		t.Fatalf("err = %q", m["err"])
	}
	if len(seen) == 0 || seen[len(seen)-1] != "--json" {
		t.Fatalf("--json not appended: %v", seen)
	}
	// the program's own json:false wins
	seen = nil
	eng.Run(context.Background(), `await ops["zz-js"]({json: false})`, "<t>")
	if len(seen) == 0 || seen[len(seen)-1] != "--json=false" {
		t.Fatalf("json:false not honoured: %v", seen)
	}
}
