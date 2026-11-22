package groups

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/muthuishere/clicodemode/codemode"
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
;({ a: a.ok, b: b.ok })`, "<t>")
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
