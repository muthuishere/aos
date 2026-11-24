package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

// stubs replaces the body of real commands (their names, usage lines and flag
// checks stay real) with a recorder, so a module runs against aos's own
// operations without touching the machine.
type stubs struct {
	mu    sync.Mutex
	calls []string
	out   map[string]string // route -> stdout
	fail  map[string]bool   // route -> exit 1
}

func newStubbedCtx(t *testing.T, s *stubs) *cli.Ctx {
	t.Helper()
	c, _ := registeredCtx("darwin", nil)
	for _, cmd := range c.Registry.Commands() {
		route := cmd.Route()
		cmd.Run = func(c *cli.Ctx, args []string) error {
			s.mu.Lock()
			s.calls = append(s.calls, route+" "+strings.Join(args, " "))
			out, failed := s.out[route], s.fail[route]
			s.mu.Unlock()
			if failed {
				fmt.Fprint(c.Stderr, "stub: "+route+" failed")
				return &cli.ExitError{Code: 1}
			}
			fmt.Fprint(c.Stdout, out)
			return nil
		}
	}
	return c
}

func runModule(t *testing.T, c *cli.Ctx, src string) (any, string) {
	t.Helper()
	r := newCodeEngine(c).Run(context.Background(), src, "<t>")
	if !r.OK {
		return nil, r.Error
	}
	b, _ := json.Marshal(r.Value)
	var v any
	_ = json.Unmarshal(b, &v)
	return v, ""
}

func (s *stubs) has(t *testing.T, want string) {
	t.Helper()
	for _, c := range s.calls {
		if strings.Contains(c, want) {
			return
		}
	}
	t.Errorf("no call containing %q in %v", want, s.calls)
}

func TestCodeModulesCheckClean(t *testing.T) {
	c, _ := registeredCtx("linux", nil)
	eng := newCodeEngine(c)
	if ds := eng.CheckModules(); len(ds) != 0 {
		t.Fatalf("module diagnostics: %v", ds)
	}
	for _, name := range []string{"aos:layout", "aos:ui", "aos:snapshot"} {
		if m, ok := eng.Registry.LookupModule(name); !ok || m.Description == "" {
			t.Errorf("%s: missing or no description", name)
		} else {
			for _, e := range m.Info().Exports {
				if e.Doc == "" {
					t.Errorf("%s: export %s has no doc comment", name, e.Name)
				}
			}
		}
	}
}

func TestCodeModuleLayout(t *testing.T) {
	s := &stubs{}
	c := newStubbedCtx(t, s)
	v, err := runModule(t, c, `import { sideBySide, tile } from "aos:layout"
const a = await sideBySide("Code", "Safari", 2)
const b = await tile(["A","B","C","D"])
;({ a, b: b.map(x => x.zone) })`)
	if err != "" {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if fmt.Sprint(m["b"]) != "[2A 2B 2C 2D]" {
		t.Errorf("zones %v", m["b"])
	}
	s.has(t, "window move Code --monitor=2 --zone=1A")
	s.has(t, "window move Safari --monitor=2 --zone=1B")
	if _, err := runModule(t, c, `import { tile } from "aos:layout"
await tile(["1","2","3","4","5"])`); !strings.Contains(err, "1 to 4 apps") {
		t.Errorf("five apps must be refused, got %q", err)
	}
}

func TestCodeModuleUI(t *testing.T) {
	s := &stubs{out: map[string]string{"window list": `[{"ID":"7","App":"Notepad","Title":"x"}]`}}
	c := newStubbedCtx(t, s)
	v, err := runModule(t, c, `import { openApp, waitForText, clickThenExpect } from "aos:ui"
const w = await openApp("Notepad", 3000)
const seen = await waitForText("Notepad", "Saved", 1)
const r = await clickThenExpect("Notepad", "Save", "Saved", 1)
;({ id: w.ID, seen, r })`)
	if err != "" {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["id"] != "7" || m["seen"] != true || fmt.Sprint(m["r"]) != "map[appeared:true clicked:true]" {
		t.Errorf("got %v", m)
	}
	s.has(t, "launch Notepad --or-focus")
	s.has(t, "window wait Notepad --timeout=3000")
	s.has(t, "watch window --app=Notepad --match=Saved --timeout=1s")
	s.has(t, "mouse click --app=Notepad --find=Save")

	// a window that never appears is null; text that never appears is false
	s2 := &stubs{fail: map[string]bool{"window wait": true, "watch window": true}}
	c2 := newStubbedCtx(t, s2)
	v, err = runModule(t, c2, `import { openApp, waitForText } from "aos:ui"
;({ w: await openApp("Nope", 10), t: await waitForText("Nope", "x", 1) })`)
	if err != "" {
		t.Fatal(err)
	}
	if m := v.(map[string]any); m["w"] != nil || m["t"] != false {
		t.Errorf("got %v", m)
	}
}

func TestCodeModuleSnapshot(t *testing.T) {
	s := &stubs{out: map[string]string{
		"window list":    `[{"App":"A","Focused":false},{"App":"B","Focused":true}]`,
		"mouse position": "12 34\n",
		"process list":   `[{"pid":1,"name":"p","cpu":9.5,"memory_mb":1}]`,
	}}
	c := newStubbedCtx(t, s)
	v, err := runModule(t, c, `import { snapshot, shotEach } from "aos:snapshot"
const s = await snapshot(3)
const shots = await shotEach(["Code Editor", "Safari"], "/tmp/x")
;({ n: s.windows.length, f: s.focused.App, m: s.mouse, p: s.busiest[0].name, shots })`)
	if err != "" {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["n"] != 2.0 || m["f"] != "B" || m["p"] != "p" || fmt.Sprint(m["m"]) != "map[x:12 y:34]" {
		t.Errorf("got %v", m)
	}
	s.has(t, "process list --sort=cpu --top=3")
	s.has(t, "capture screenshot window --app=Code Editor --out=/tmp/x/Code-Editor.png")
}
