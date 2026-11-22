package groups

// RUN-phase golden tests for `aos batch`: they drive runBatch through the real
// registry and cli.Run, with fake commands registered in the test. `say` runs
// as a child process of os.Executable() (the test binary here), so TestMain
// below turns that child into a fake speaker.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

const fakeSayLogEnv = "AOS_TEST_FAKE_SAY_LOG"

func TestMain(m *testing.M) {
	// The say child process: `<test binary> say <text> ...` with the marker env.
	if marker := os.Getenv(sayStartedEnv); marker != "" && len(os.Args) > 1 && os.Args[1] == "say" {
		fakeSayChild(marker)
		return
	}
	os.Exit(m.Run())
}

// fakeSayChild pretends to render speech, records when it was "heard", then
// signals the parent through the marker file (0 seconds: no paced typing).
func fakeSayChild(marker string) {
	time.Sleep(150 * time.Millisecond) // speech "rendering"
	if log := os.Getenv(fakeSayLogEnv); log != "" {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "heard %d %s\n", time.Now().UnixNano(), strings.Join(os.Args[2:], " "))
			f.Close()
		}
	}
	_ = os.WriteFile(marker, []byte("0"), 0o644)
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(strings.Join(os.Args[2:], " "), "failspeech") {
		os.Exit(3)
	}
	os.Exit(0)
}

// fakeCalls records which fake commands ran, in order, with start times.
type fakeCalls struct {
	mu    sync.Mutex
	names []string
	at    map[string]int64
}

func (f *fakeCalls) add(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names = append(f.names, name)
	f.at[name] = time.Now().UnixNano()
}

func (f *fakeCalls) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.names...)
}

// batchRunCtx is a context on the real registry plus fake commands:
//
//	zzfake ok <name>    prints "out <name>", exit 0
//	zzfake fail <name>  prints "boom <name>" on stderr, exit 4
//	zzfake slow <name>  sleeps 80ms, exit 0
func batchRunCtx(t *testing.T) (*cli.Ctx, *bytes.Buffer, *bytes.Buffer, *fakeCalls) {
	t.Helper()
	calls := &fakeCalls{at: map[string]int64{}}
	r := cli.NewRegistry()
	Register(r)
	r.Describe("zzfake", "test-only fake commands")
	name := func(args []string) string { return strings.Join(args, " ") }
	r.Add(
		&cli.Command{Group: "zzfake", Name: "ok", Summary: "ok", Args: "<name>", Run: func(c *cli.Ctx, args []string) error {
			calls.add(name(args))
			c.Printf("out %s\n", name(args))
			return nil
		}},
		&cli.Command{Group: "zzfake", Name: "fail", Summary: "fail", Args: "<name>", Run: func(c *cli.Ctx, args []string) error {
			calls.add(name(args))
			fmt.Fprintf(c.Stderr, "boom %s\n", name(args))
			return &cli.ExitError{Code: 4}
		}},
		&cli.Command{Group: "zzfake", Name: "slow", Summary: "slow", Args: "<name>", Run: func(c *cli.Ctx, args []string) error {
			time.Sleep(80 * time.Millisecond)
			calls.add(name(args))
			return nil
		}},
	)
	var out, errb bytes.Buffer
	c := &cli.Ctx{
		Registry: r, Stdin: io.NopCloser(strings.NewReader("")), Stdout: &out, Stderr: &errb,
		Env: func(string) string { return "" }, GOOS: "linux", Version: "test",
	}
	return c, &out, &errb, calls
}

func runBatchArgs(c *cli.Ctx, args ...string) int {
	return cli.Run(c, append([]string{"batch"}, args...))
}

// jsonLines decodes JSONL keeping each object's key order.
func jsonLines(t *testing.T, s string) [][]string {
	t.Helper()
	var keys [][]string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		dec := json.NewDecoder(strings.NewReader(line))
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			t.Fatalf("not a JSON object: %q", line)
		}
		var k []string
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				t.Fatalf("%q: %v", line, err)
			}
			k = append(k, tok.(string))
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				t.Fatalf("%q: %v", line, err)
			}
		}
		keys = append(keys, k)
	}
	return keys
}

func decodeLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// normalizeTimes replaces the ms/at_ms values with 0 after checking they are
// non-negative whole numbers.
func normalizeTimes(t *testing.T, lines []map[string]any) {
	t.Helper()
	for i, m := range lines {
		for _, k := range []string{"ms", "at_ms"} {
			v, ok := m[k]
			if !ok {
				continue
			}
			f, isNum := v.(float64)
			if !isNum || f < 0 || f != float64(int64(f)) {
				t.Errorf("line %d: %s = %v (%T), want a non-negative integer", i, k, v, v)
			}
			m[k] = float64(0)
		}
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestBatchRunJSONGolden(t *testing.T) {
	c, out, errb, calls := batchRunCtx(t)
	code := runBatchArgs(c, "--json", "[zzfake-ok: a, wait: 1ms, do: zzfake ok b]")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	keys := jsonLines(t, out.String())
	leafKeys := "step,run,ok,exit,ms,at_ms,output"
	waitKeys := "step,run,ok,exit,ms,at_ms" // output is omitempty
	sumKeys := "ms,ok,ran,steps,summary"    // a map: encoding/json sorts the keys
	want := []string{leafKeys, waitKeys, leafKeys, sumKeys}
	if len(keys) != len(want) {
		t.Fatalf("%d lines, want %d:\n%s", len(keys), len(want), out.String())
	}
	for i := range want {
		if got := strings.Join(keys[i], ","); got != want[i] {
			t.Errorf("line %d keys %s, want %s", i, got, want[i])
		}
	}
	lines := decodeLines(t, out.String())
	normalizeTimes(t, lines)
	golden := []string{
		`{"at_ms":0,"exit":0,"ms":0,"ok":true,"output":"out a","run":"zzfake ok a","step":"1"}`,
		`{"at_ms":0,"exit":0,"ms":0,"ok":true,"run":"wait 1ms","step":"2"}`,
		`{"at_ms":0,"exit":0,"ms":0,"ok":true,"output":"out b","run":"zzfake ok b","step":"3"}`,
		`{"ms":0,"ok":true,"ran":3,"steps":3,"summary":true}`,
	}
	for i, g := range golden {
		if got := mustJSON(lines[i]); got != g {
			t.Errorf("line %d:\n got %s\nwant %s", i, got, g)
		}
	}
	if got := strings.Join(calls.list(), ","); got != "a,b" {
		t.Errorf("ran %q", got)
	}
}

func TestBatchRunWaitStep(t *testing.T) {
	c, out, _, _ := batchRunCtx(t)
	start := time.Now()
	if code := runBatchArgs(c, "--json", "[wait: 30ms]"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if time.Since(start) < 30*time.Millisecond {
		t.Errorf("wait did not wait")
	}
	lines := decodeLines(t, out.String())
	if lines[0]["run"] != "wait 30ms" || lines[0]["ok"] != true || lines[0]["ms"].(float64) < 30 {
		t.Errorf("wait line %v", lines[0])
	}
	if _, has := lines[0]["output"]; has {
		t.Errorf("wait has output: %v", lines[0])
	}
}

func TestBatchRunStopsAtFirstFailure(t *testing.T) {
	c, out, _, calls := batchRunCtx(t)
	code := runBatchArgs(c, "--json", "[zzfake-ok: a, zzfake-fail: b, zzfake-ok: c]")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	lines := decodeLines(t, out.String())
	normalizeTimes(t, lines)
	golden := []string{
		`{"at_ms":0,"exit":0,"ms":0,"ok":true,"output":"out a","run":"zzfake ok a","step":"1"}`,
		`{"at_ms":0,"exit":4,"ms":0,"ok":false,"output":"boom b","run":"zzfake fail b","step":"2"}`,
		`{"ms":0,"ok":false,"ran":2,"steps":3,"summary":true}`,
	}
	if len(lines) != len(golden) {
		t.Fatalf("%d lines, want %d:\n%s", len(lines), len(golden), out.String())
	}
	for i, g := range golden {
		if got := mustJSON(lines[i]); got != g {
			t.Errorf("line %d:\n got %s\nwant %s", i, got, g)
		}
	}
	if got := strings.Join(calls.list(), ","); got != "a,b" {
		t.Errorf("ran %q; step 3 must not run", got)
	}
}

func TestBatchRunRefusesOnCheckErrors(t *testing.T) {
	for _, json := range []bool{false, true} {
		c, out, errb, calls := batchRunCtx(t)
		args := []string{"[zzfake-ok: a, lanch: calc, wait: soon]"}
		if json {
			args = append([]string{"--json"}, args...)
		}
		code := runBatchArgs(c, args...)
		if code != 2 {
			t.Errorf("json=%v: exit %d, want 2", json, code)
		}
		if out.Len() != 0 {
			t.Errorf("json=%v: stdout %q, want nothing", json, out.String())
		}
		e := errb.String()
		for _, want := range []string{`unknown verb "lanch"`, `"soon" is not a duration`, "aos: 2 error(s); nothing was run"} {
			if !strings.Contains(e, want) {
				t.Errorf("json=%v: stderr missing %q:\n%s", json, want, e)
			}
		}
		if n := len(calls.list()); n != 0 {
			t.Errorf("json=%v: %d leaves ran, want 0", json, n)
		}
	}
}

func TestBatchRunParallelGroup(t *testing.T) {
	c, out, _, calls := batchRunCtx(t)
	code := runBatchArgs(c, "--json", "[zzfake-ok: a, parallel: [zzfake-fail: b, zzfake-slow: c, zzfake-ok: d], zzfake-ok: e]")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	lines := decodeLines(t, out.String())
	normalizeTimes(t, lines)
	if len(lines) != 5 {
		t.Fatalf("%d lines, want 5 (1, three children, summary):\n%s", len(lines), out.String())
	}
	if got := mustJSON(lines[0]); got != `{"at_ms":0,"exit":0,"ms":0,"ok":true,"output":"out a","run":"zzfake ok a","step":"1"}` {
		t.Errorf("line 0 %s", got)
	}
	// the children finish in any order; the slow one last
	children := map[string]string{}
	for _, l := range lines[1:4] {
		children[l["step"].(string)] = mustJSON(l)
	}
	wantChildren := map[string]string{
		"2.1": `{"at_ms":0,"exit":4,"ms":0,"ok":false,"output":"boom b","run":"zzfake fail b","step":"2.1"}`,
		"2.2": `{"at_ms":0,"exit":0,"ms":0,"ok":true,"run":"zzfake slow c","step":"2.2"}`,
		"2.3": `{"at_ms":0,"exit":0,"ms":0,"ok":true,"output":"out d","run":"zzfake ok d","step":"2.3"}`,
	}
	for id, w := range wantChildren {
		if children[id] != w {
			t.Errorf("%s:\n got %s\nwant %s", id, children[id], w)
		}
	}
	if lines[3]["step"] != "2.2" {
		t.Errorf("slow child should report last, got %v", lines[3]["step"])
	}
	if got := mustJSON(lines[4]); got != `{"ms":0,"ok":false,"ran":4,"steps":3,"summary":true}` {
		t.Errorf("summary %s", got)
	}
	got := calls.list()
	if strings.Join(got, ",") == "" || len(got) != 4 || got[0] != "a" {
		t.Errorf("ran %q", got)
	}
	for _, n := range got {
		if n == "e" {
			t.Errorf("step 3 ran after the failed parallel group")
		}
	}
}

func TestBatchRunTextMode(t *testing.T) {
	c, out, errb, _ := batchRunCtx(t)
	code := runBatchArgs(c, "[zzfake-ok: a, wait: 1ms, zzfake-fail: b, zzfake-ok: c]")
	if code != 1 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	ms := regexp.MustCompile(`(?m)[ \d]{5}ms$`)
	secs := regexp.MustCompile(`in \d+\.\ds$`)
	got := secs.ReplaceAllString(ms.ReplaceAllString(strings.TrimRight(out.String(), "\n"), "    Nms"), "in N.Ns")
	want := strings.Join([]string{
		fmt.Sprintf("ok   %-5s %-50s", "1", "zzfake ok a") + "     Nms",
		fmt.Sprintf("ok   %-5s %-50s", "2", "wait 1ms") + "     Nms",
		fmt.Sprintf("FAIL %-5s %-50s", "3", "zzfake fail b") + "     Nms",
		"     boom b",
		"stopped: 3 step(s) in N.Ns",
	}, "\n")
	if got != want {
		t.Errorf("text output:\n got %q\nwant %q", got, want)
	}
	// the duration column is right-aligned to five digits
	if !regexp.MustCompile(`(?m)^ok   1     zzfake ok a {40}[ \d]{4}\dms$`).MatchString(out.String()) {
		t.Errorf("column layout:\n%s", out.String())
	}

	c, out, _, _ = batchRunCtx(t)
	if code := runBatchArgs(c, "[zzfake-ok: a]"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !regexp.MustCompile(`\ndone: 1 step\(s\) in \d+\.\ds\n$`).MatchString(out.String()) {
		t.Errorf("done line:\n%q", out.String())
	}
}

func TestBatchRunSayStartsFirst(t *testing.T) {
	log := filepath.Join(t.TempDir(), "say.log")
	t.Setenv(fakeSayLogEnv, log)
	c, out, errb, calls := batchRunCtx(t)
	code := runBatchArgs(c, "--json", "[parallel: [zzfake-ok: x, say: hello there, zzfake-ok: y]]")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q\n%s", code, errb.String(), out.String())
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the fake say child never ran: %v", err)
	}
	var heard int64
	var text string
	if _, err := fmt.Sscanf(string(data), "heard %d", &heard); err != nil {
		t.Fatalf("say log %q", data)
	}
	text = strings.TrimSpace(strings.SplitN(string(data), " ", 3)[2])
	if text != "hello there" {
		t.Errorf("say child got %q", text)
	}
	for _, n := range []string{"x", "y"} {
		if calls.at[n] < heard {
			t.Errorf("%s started %v before the speech was heard", n, time.Duration(heard-calls.at[n]))
		}
	}
	lines := decodeLines(t, out.String())
	steps := map[string]map[string]any{}
	for _, l := range lines[:3] {
		steps[l["step"].(string)] = l
	}
	if s := steps["1.2"]; s == nil || s["run"] != "say hello there" || s["ok"] != true {
		t.Errorf("say line %v", s)
	}
	if steps["1.1"] == nil || steps["1.3"] == nil {
		t.Errorf("missing children: %s", out.String())
	}
	if !lines[3]["summary"].(bool) || lines[3]["ran"].(float64) != 3 {
		t.Errorf("summary %v", lines[3])
	}
}

func TestBatchRunSayFailureFailsGroup(t *testing.T) {
	t.Setenv(fakeSayLogEnv, filepath.Join(t.TempDir(), "say.log"))
	c, out, _, calls := batchRunCtx(t)
	code := runBatchArgs(c, "--json", "[parallel: [say: failspeech, zzfake-ok: x], zzfake-ok: after]")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out.String())
	}
	lines := decodeLines(t, out.String())
	var say map[string]any
	for _, l := range lines {
		if l["step"] == "1.1" {
			say = l
		}
	}
	if say == nil || say["ok"] != false || say["exit"].(float64) != 1 || !strings.Contains(say["output"].(string), "exit status 3") {
		t.Errorf("say failure line %v", say)
	}
	if got := strings.Join(calls.list(), ","); got != "x" {
		t.Errorf("ran %q, want x only", got)
	}
}
