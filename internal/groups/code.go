package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/clicodemode/codemode"

	"github.com/muthuishere/aos/internal/cli"
)

// `aos code` is the opt-in Code Mode: one small program that calls many aos
// operations, instead of many aos processes. Every operation is a batch verb
// (same names, same table, from batchVerbs) and its handler runs the same
// cli.Invoke path as a batch step, so the two can never disagree about what a
// command is. `aos batch` itself is untouched.
//
// Not exposed, same as batch: batchNoVerb groups and batchSystemGroups
// (power, system, pkg, service, network). External exec and shell from a
// program are off (empty codemode.ExecPolicy): aos's own commands are the
// whole surface. The interpreter is not a sandbox for hostile code;
// operations run with aos's own privileges.

func init() {
	// A batch cannot run `aos code`, and code must not list itself. batch.go
	// is not edited; its table is a plain map read at run time.
	batchNoVerb["code"] = true

	register(func(r *cli.Registry) {
		r.Describe("code", "Run one small program that calls many aos commands (opt-in Code Mode)")
		r.Add(&cli.Command{
			Group: "code", Name: "",
			Summary: "Discover operations and run JavaScript programs that call them in parallel; one process, only the final result returned",
			Args:    "operations | describe <op> | capabilities --json | docs | check <prog.js> | run <prog.js|-> | eval '<expr>' | skill",
			Examples: []string{
				"aos code operations",
				"aos code describe capture-screenshot",
				`aos code eval 'await ops["window-list"]({ args: ["--json"] })'`,
				"aos code run prog.js",
			},
			Run: runCode,
		})
	})
}

func runCode(c *cli.Ctx, args []string) error {
	eng := newCodeEngine(c)
	cm := codemode.NewCLI(eng, "aos code")
	cm.Stdin, cm.Stdout, cm.Stderr = c.Stdin, c.Stdout, c.Stderr
	if code := cm.Main(context.Background(), args); code != 0 {
		return &cli.ExitError{Code: code}
	}
	return nil
}

func newCodeEngine(c *cli.Ctx) *codemode.Engine {
	reg := codemode.NewRegistry(c.Version)
	for _, op := range codeOperations(c) {
		reg.MustRegister(op)
	}
	eng := codemode.NewEngine(reg)
	eng.Stdout = c.Stderr // print() and console.log go to stderr, never into the result
	return eng
}

// codeOperations is one operation per batch verb, named as batch names it.
func codeOperations(c *cli.Ctx) []codemode.Operation {
	verbs := batchVerbs(c.Registry)
	names := make([]string, 0, len(verbs))
	for n := range verbs {
		if n == "do" { // `do` runs any command as typed, which would bypass the exclusions
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	ops := make([]codemode.Operation, 0, len(names))
	for _, n := range names {
		v := verbs[n]
		ops = append(ops, codemode.Operation{
			Name:        n,
			Description: codeDescription(v),
			InputSchema: codeSchema(v),
			SideEffects: codemode.SideEffectLocal,
			Handler:     codeHandler(c, v),
		})
	}
	return ops
}

func codeDescription(v *batchVerb) string {
	return v.Summary + "  [" + v.Usage + "]"
}

// codeSchema: `args` are the positional words; every flag the command
// documents is an optional property. A command that documents no flags is not
// policed (batch does the same), so unknown keys pass through as flags.
func codeSchema(v *batchVerb) json.RawMessage {
	props := map[string]any{
		"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "positional words"},
	}
	for f := range v.flags {
		props[f] = map[string]any{"type": []string{"string", "number", "boolean"}, "description": "--" + f}
	}
	s := map[string]any{"type": "object", "properties": props}
	if v.flags != nil {
		s["additionalProperties"] = false
	}
	b, _ := json.Marshal(s)
	return b
}

// codeArgv is the argv a verb call runs: the same words batch builds.
func codeArgv(v *batchVerb, in map[string]any) ([]string, error) {
	var words []string
	switch a := in["args"].(type) {
	case nil:
	case []any:
		for _, x := range a {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("args must be strings, got %v", x)
			}
			words = append(words, s)
		}
	default:
		return nil, fmt.Errorf("args must be an array of strings")
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		if k != "args" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var fl []string
	for _, k := range keys {
		switch x := in[k].(type) {
		case bool:
			if x {
				fl = append(fl, "--"+k)
			} else {
				fl = append(fl, "--"+k+"=false") // as batch emits it
			}
		case string:
			fl = append(fl, "--"+k+"="+x)
		case float64:
			fl = append(fl, "--"+k+"="+strconv.FormatFloat(x, 'f', -1, 64))
		default:
			return nil, fmt.Errorf("--%s must be a string, number or boolean", k)
		}
	}
	if v.cmd != nil {
		return append(append(append([]string{}, v.Route...), words...), fl...), nil
	}
	return codeShortArgv(v.Name, words, fl)
}

// codeShortArgv builds the short verbs (click, type, key, screenshot, say)
// the way batch.build does; TestCodeArgvMatchesBatch holds the two together.
func codeShortArgv(name string, words, fl []string) ([]string, error) {
	need := func(n int, what string) error {
		if len(words) < n {
			return fmt.Errorf("%s needs %s", name, what)
		}
		return nil
	}
	isPair := len(words) == 2 && isInt(words[0]) && isInt(words[1])
	switch name {
	case "click":
		if isPair {
			return append([]string{"mouse", "click", words[0], words[1]}, fl...), nil
		}
		if err := need(2, "an app and the text to click"); err != nil {
			return nil, err
		}
		return append([]string{"mouse", "click", "--app=" + words[0], "--find=" + strings.Join(words[1:], " ")}, fl...), nil
	case "type":
		if err := need(1, "text to type"); err != nil {
			return nil, err
		}
		if len(words) == 1 {
			return append([]string{"key", "type", words[0]}, fl...), nil
		}
		return append([]string{"key", "type", strings.Join(words[1:], " "), "--app=" + words[0]}, fl...), nil
	case "key":
		if err := need(1, "a key combination"); err != nil {
			return nil, err
		}
		if len(words) > 2 {
			return nil, fmt.Errorf("key takes an app and one combination")
		}
		out := []string{"key", "press", words[len(words)-1]}
		if len(words) == 2 {
			out = append(out, "--app="+words[0])
		}
		return append(out, fl...), nil
	case "screenshot":
		if len(words) > 1 {
			return nil, fmt.Errorf("screenshot takes one app")
		}
		if len(words) == 1 {
			return append([]string{"capture", "screenshot", "window", "--app=" + words[0]}, fl...), nil
		}
		return append([]string{"capture", "screenshot"}, fl...), nil
	case "say":
		if err := need(1, "text to say"); err != nil {
			return nil, err
		}
		return append([]string{"say", strings.Join(words, " ")}, fl...), nil
	}
	return nil, fmt.Errorf("unknown verb %s", name)
}

// codeJSONFlag matches a documented --json flag in a command's usage line.
var codeJSONFlag = regexp.MustCompile(`--json\b`)

// codeHandler is JSON-first: when the verb documents --json it is added
// (unless the program set it), and a call returns the parsed JSON value itself,
// or the trimmed stdout when there is no JSON. A non-zero exit rejects with
// "exit N: <stderr tail>", so a program uses try/catch instead of checking ok.
func codeHandler(c *cli.Ctx, v *batchVerb) codemode.Handler {
	hasJSON := v.cmd != nil && codeJSONFlag.MatchString(v.cmd.Args)
	return func(ctx context.Context, in map[string]any) (any, error) {
		if v.Name == "wait" {
			return codeWait(ctx, in)
		}
		argv, err := codeArgv(v, in)
		if err != nil {
			return nil, err
		}
		if _, set := in["json"]; hasJSON && !set {
			argv = append(argv, "--json")
		}
		out := cli.Invoke(c, argv, "")
		if out.Exit != 0 {
			return nil, fmt.Errorf("exit %d: %s", out.Exit, codeTail(out.Stderr, out.Stdout))
		}
		var j any
		if json.Unmarshal([]byte(out.Stdout), &j) == nil && strings.TrimSpace(out.Stdout) != "" {
			return j, nil
		}
		return strings.TrimSpace(out.Stdout), nil
	}
}

// codeTail is the end of the first non-empty text, capped, for an error message.
func codeTail(texts ...string) string {
	for _, t := range texts {
		if t = strings.TrimSpace(t); t != "" {
			if len(t) > 500 {
				t = "..." + t[len(t)-500:]
			}
			return t
		}
	}
	return "no output"
}

// codeWait pauses for a duration, as batch's wait: does (500 ms, 2s, 1m); the
// program can also just await its own timers, this keeps the batch word.
func codeWait(ctx context.Context, in map[string]any) (any, error) {
	words, _ := in["args"].([]any)
	if len(words) != 1 {
		return nil, fmt.Errorf("wait takes one duration: 500 (ms), 2s, 1m")
	}
	s, _ := words[0].(string)
	d, err := batchDuration(s)
	if err != nil {
		return nil, err
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return "", nil
}
