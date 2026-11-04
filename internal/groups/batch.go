package groups

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/muthuishere/aos/internal/cli"
)

// `aos batch` runs a list of aos steps from YAML (a file, `-` for stdin, or
// the YAML itself as the argument). Spec: docs/batch-spec.md.
//
// A step is one verb. Every aos command is a verb named <group>-<command>
// ("window-snap", "capture-video-start"), with the CLI's own words and flags,
// plus a few short names (click, type, key, screenshot, say, wait) and `do:` for any command as typed. `parallel:` runs its list
// together. The verbs come from the live command registry, so the batch can
// never disagree with the CLI. The whole script is checked before step 1.

// batchSystemGroups change the machine itself; they are not verbs, and reaching
// them through `do:` draws a warning.
var batchSystemGroups = map[string]bool{"power": true, "system": true, "pkg": true, "service": true, "network": true}

// batchNoVerb are groups a batch cannot sensibly run as a step.
var batchNoVerb = map[string]bool{"batch": true, "serve": true, "completion": true, "skill": true, "debug": true}

// batchShortNames are the verbs that are not a <group>-<command>.
var batchShortNames = map[string]string{
	"click":      "mouse click --app=<app> --find=<target>  (or: click: <x> <y>)",
	"type":       "key type <text> [--app=<app>]  (click: Notepad hello world)",
	"key":        "key press <combo> [--app=<app>]",
	"screenshot": "capture screenshot window --app=<app>  (no app: the whole screen)",
	"say":        "speak the text and show it as a subtitle (say: hello there [--voice=Zira] [--size=L])",
	"wait":       "pause for a duration: 500 (ms), 2s, 1m",
	"do":         "any aos command, as typed (do: window snap Notepad left)",
}

// batchLongForm names the positional slots of the short names, for the map
// form: click: {app: Calculator, target: Seven}.
var batchLongForm = map[string][]string{
	"click": {"app", "target"}, "type": {"app", "text"}, "key": {"app", "combo"},
	"screenshot": {"app"}, "say": {"text"}, "wait": {"time"}, "do": {"command"},
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("batch", "Run many aos steps from YAML: in order, or together with parallel")
		r.Add(
			&cli.Command{
				Group: "batch", Name: "",
				Summary: "Run a YAML list of steps (a file, - for stdin, or the YAML inline); everything is checked first",
				Args:    "<file.yaml | - | inline yaml> [--check] [--json]",
				Examples: []string{
					"aos batch demo.yaml",
					"aos batch --check demo.yaml",
					`aos batch '[launch: calc, parallel: [say: seven, click: Calculator Seven], type: Notepad done]'`,
					"aos batch verbs   # every verb, generated from the commands",
				},
				Run: runBatch,
			},
			&cli.Command{
				Group: "batch", Name: "verbs",
				Summary:  "List every verb a batch step can use",
				Args:     "[--json]",
				Examples: []string{"aos batch verbs", "aos batch verbs --json"},
				Run:      runBatchVerbs,
			},
		)
	})
}

// ---- verbs ------------------------------------------------------------------

type batchVerb struct {
	Name    string   `json:"name"`
	Route   []string `json:"route,omitempty"` // the aos words it runs; nil for short names
	Usage   string   `json:"usage"`
	Summary string   `json:"summary"`
	flags   map[string]bool
	cmd     *cli.Command
}

var flagRE = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)

// batchVerbs derives the verb table from the registry.
func batchVerbs(r *cli.Registry) map[string]*batchVerb {
	out := map[string]*batchVerb{}
	for _, c := range r.Commands() {
		if c.Hidden || batchNoVerb[c.Group] || batchSystemGroups[c.Group] || c.Run == nil {
			continue
		}
		name := c.Group
		route := []string{c.Group}
		if c.Name != "" {
			name += "-" + strings.ReplaceAll(c.Name, " ", "-")
			route = append(route, strings.Fields(c.Name)...)
		}
		v := &batchVerb{Name: name, Route: route, Usage: strings.TrimSpace("aos " + c.Route() + " " + c.Args), Summary: c.Summary, cmd: c}
		v.flags = flagSet(c.Args)
		out[name] = v
	}
	for name, usage := range batchShortNames {
		out[name] = &batchVerb{Name: name, Usage: usage, Summary: usage}
	}
	// say takes its own flags plus every subtitle style flag (one list).
	sayFlags := map[string]bool{"json": true, "no-subtitle": true, "narrate": true}
	for _, f := range append(append([]string{}, sayValueFlags...), sayCaptionFlags()...) {
		sayFlags[f] = true
	}
	out["say"].flags = sayFlags
	out["click"].flags = map[string]bool{"double": true, "button": true, "role": true, "focus": true, "force": true}
	out["type"].flags = map[string]bool{"verify": true, "focus": true}
	out["key"].flags = map[string]bool{"focus": true, "force": true}
	out["screenshot"].flags = map[string]bool{"out": true, "copy": true}
	return out
}

func flagSet(args string) map[string]bool {
	m := map[string]bool{}
	for _, f := range flagRE.FindAllStringSubmatch(args, -1) {
		m[f[1]] = true
	}
	if len(m) == 0 {
		return nil // the command documents no flags: do not police them
	}
	m["json"] = true
	return m
}

func runBatchVerbs(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	verbs := batchVerbs(c.Registry)
	names := make([]string, 0, len(verbs))
	for n := range verbs {
		names = append(names, n)
	}
	sort.Strings(names)
	if set.Has("json") {
		list := make([]*batchVerb, 0, len(names))
		for _, n := range names {
			list = append(list, verbs[n])
		}
		return json.NewEncoder(c.Stdout).Encode(list)
	}
	for _, n := range names {
		c.Printf("%-26s %s\n", n, verbs[n].Usage)
	}
	return nil
}

// ---- parsing and checking ---------------------------------------------------

// batchStep is one checked step: an argv to run, a duration to wait, text to
// say, or a parallel group.
type batchStep struct {
	At       string
	Verb     string
	Argv     []string // aos words (without "aos")
	Say      []string // `say` arguments
	Wait     time.Duration
	Parallel []*batchStep
}

func (s *batchStep) String() string {
	switch {
	case s.Parallel != nil:
		var parts []string
		for _, p := range s.Parallel {
			parts = append(parts, p.String())
		}
		return "parallel [" + strings.Join(parts, " | ") + "]"
	case s.Verb == "wait":
		return "wait " + s.Wait.String()
	case s.Verb == "say":
		return "say " + strings.Join(s.Say, " ")
	}
	return strings.Join(s.Argv, " ")
}

type batchFinding struct {
	Severity string `json:"severity"` // error | warning
	At       string `json:"at"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

func (f batchFinding) String() string {
	s := fmt.Sprintf("%s: %s: %s", f.At, f.Severity, f.Message)
	if f.Hint != "" {
		s += " (" + f.Hint + ")"
	}
	return s
}

type batchChecker struct {
	reg     *cli.Registry
	verbs   map[string]*batchVerb
	fs      []batchFinding
	caption []batchFlag // the top-level subtitle: block, defaults for say steps
}

func (b *batchChecker) errorf(at, hint, format string, a ...any) {
	b.fs = append(b.fs, batchFinding{Severity: "error", At: at, Message: fmt.Sprintf(format, a...), Hint: hint})
}

func (b *batchChecker) warnf(at, format string, a ...any) {
	b.fs = append(b.fs, batchFinding{Severity: "warning", At: at, Message: fmt.Sprintf(format, a...)})
}

func pos(n *yaml.Node) string { return fmt.Sprintf("%d:%d", n.Line, n.Column) }

// checkBatch parses and checks a whole script without running anything.
func checkBatch(reg *cli.Registry, src []byte) ([]*batchStep, []batchFinding) {
	b := &batchChecker{reg: reg, verbs: batchVerbs(reg)}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		msg := strings.TrimPrefix(err.Error(), "yaml: ")
		at := "1:1"
		if rest, ok := strings.CutPrefix(msg, "line "); ok {
			if n, tail, ok := strings.Cut(rest, ":"); ok {
				at, msg = n+":1", strings.TrimSpace(tail)
			}
		}
		b.errorf(at, "", "%s", msg)
		return nil, b.fs
	}
	if len(doc.Content) == 0 {
		b.errorf("1:1", "", "no steps")
		return nil, b.fs
	}
	root := doc.Content[0]
	list := root
	if root.Kind == yaml.MappingNode {
		list = nil
		for i := 0; i+1 < len(root.Content); i += 2 {
			k := root.Content[i]
			switch k.Value {
			case "steps":
				list = root.Content[i+1]
				continue
			case "subtitle":
				b.captionDefaults(root.Content[i+1])
				continue
			}
			b.errorf(pos(k), "a batch file has `steps:` and an optional `subtitle:` style", "unknown top-level key %q", k.Value)
		}
		if list == nil {
			b.errorf(pos(root), "", "the file needs a `steps:` list")
			return nil, b.fs
		}
	}
	steps := b.stepList(list, false)
	if len(steps) == 0 && len(b.fs) == 0 {
		b.errorf(pos(root), "", "no steps")
	}
	return steps, b.fs
}

// captionDefaults reads `subtitle: {position: bottom-center, size: L}`: the
// caption style every say step uses unless it sets its own.
func (b *batchChecker) captionDefaults(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		b.errorf(pos(n), "subtitle: {position: bottom-center, size: L}", "subtitle must be a map of caption style flags")
		return
	}
	known := sayCaptionFlags()
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !slices.Contains(known, k.Value) {
			hint := "style flags: " + strings.Join(known, ", ")
			if s := closest(k.Value, known); s != "" {
				hint = "did you mean " + s + "?"
			}
			b.errorf(pos(k), hint, "subtitle has no %q", k.Value)
			continue
		}
		if v.Kind != yaml.ScalarNode {
			b.errorf(pos(v), "", "subtitle %s must be a plain value", k.Value)
			continue
		}
		b.caption = append(b.caption, batchFlag{name: k.Value, value: v.Value, hasValue: true, at: pos(k)})
	}
}

func (b *batchChecker) stepList(n *yaml.Node, inParallel bool) []*batchStep {
	if n.Kind != yaml.SequenceNode {
		b.errorf(pos(n), "each step starts with `- `", "steps must be a list")
		return nil
	}
	var out []*batchStep
	for _, item := range n.Content {
		if st := b.step(item, inParallel); st != nil {
			out = append(out, st)
		}
	}
	return out
}

func (b *batchChecker) step(n *yaml.Node, inParallel bool) *batchStep {
	// A bare verb with no arguments: `- display-list`.
	if n.Kind == yaml.ScalarNode && n.Value != "" && !strings.ContainsAny(n.Value, " \t") {
		n = &yaml.Node{Kind: yaml.MappingNode, Line: n.Line, Column: n.Column, Content: []*yaml.Node{
			n, {Kind: yaml.ScalarNode, Tag: "!!null", Line: n.Line, Column: n.Column}}}
	}
	if n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		b.errorf(pos(n), "e.g. `- click: Calculator Seven`", "a step is `verb: arguments`")
		return nil
	}
	if len(n.Content) > 2 {
		b.errorf(pos(n.Content[2]), "put it in its own step, or list both under `parallel:` to run them together",
			"one step has two verbs (%s and %s)", n.Content[0].Value, n.Content[2].Value)
		return nil
	}
	k, v := n.Content[0], n.Content[1]
	if k.Value == "parallel" {
		if inParallel {
			b.errorf(pos(k), "list every step in one parallel", "parallel inside parallel is not supported")
			return nil
		}
		sub := b.stepList(v, true)
		if len(v.Content) == 0 {
			b.errorf(pos(k), "", "parallel needs at least one step")
		}
		return &batchStep{At: pos(k), Verb: "parallel", Parallel: sub}
	}
	verb := b.verbs[k.Value]
	if verb == nil {
		b.unknownVerb(k)
		return nil
	}
	words, flags, ok := b.words(verb, v)
	if !ok {
		return nil
	}
	return b.build(verb, k, words, flags)
}

func (b *batchChecker) unknownVerb(k *yaml.Node) {
	name := k.Value
	group, _, _ := strings.Cut(name, "-")
	if batchSystemGroups[group] {
		b.errorf(pos(k), "use `do: "+strings.ReplaceAll(name, "-", " ")+"` if you really mean it",
			"%q changes the system; system-level commands are not batch verbs", name)
		return
	}
	names := make([]string, 0, len(b.verbs))
	for n := range b.verbs {
		names = append(names, n)
	}
	hint := "see `aos batch verbs`"
	if s := closest(name, names); s != "" {
		hint = "did you mean " + s + "?"
	}
	b.errorf(pos(k), hint, "unknown verb %q", name)
}

// batchFlag is one --name[=value] with where it was written.
type batchFlag struct {
	name, value string
	hasValue    bool
	at          string
}

// words reads a verb's value: a short string, a list of words, or a map.
func (b *batchChecker) words(verb *batchVerb, v *yaml.Node) ([]string, []batchFlag, bool) {
	var words []string
	var flags []batchFlag
	add := func(w, at string) {
		if verb.Name != "do" && strings.HasPrefix(w, "--") && len(w) > 2 {
			name, val, has := strings.Cut(w[2:], "=")
			flags = append(flags, batchFlag{name, val, has, at})
			return
		}
		words = append(words, w)
	}
	switch v.Kind {
	case yaml.ScalarNode:
		if v.Tag == "!!null" {
			break
		}
		split, err := splitWords(v.Value)
		if err != nil {
			b.errorf(pos(v), "", "%v", err)
			return nil, nil, false
		}
		for _, w := range split {
			add(w, pos(v))
		}
	case yaml.SequenceNode:
		for _, it := range v.Content {
			if it.Kind != yaml.ScalarNode {
				b.errorf(pos(it), "", "list items must be plain words")
				return nil, nil, false
			}
			add(it.Value, pos(it))
		}
	case yaml.MappingNode:
		slots := batchLongForm[verb.Name]
		named := map[string]string{}
		for i := 0; i+1 < len(v.Content); i += 2 {
			fk, fv := v.Content[i], v.Content[i+1]
			if fv.Kind != yaml.ScalarNode {
				b.errorf(pos(fv), "", "%s must be a plain value", fk.Value)
				return nil, nil, false
			}
			switch {
			case slices.Contains(slots, fk.Value):
				named[fk.Value] = fv.Value
			case fk.Value == "args":
				split, err := splitWords(fv.Value)
				if err != nil {
					b.errorf(pos(fv), "", "%v", err)
					return nil, nil, false
				}
				words = append(words, split...)
			default:
				f := batchFlag{name: fk.Value, value: fv.Value, hasValue: true, at: pos(fk)}
				if fv.Value == "true" {
					f.hasValue = false
				}
				flags = append(flags, f)
			}
		}
		for _, s := range slots {
			if val, ok := named[s]; ok {
				words = append(words, val)
			} else if s != "app" && len(named) > 0 {
				b.errorf(pos(v), "long form: "+strings.Join(slots, ", "), "%s needs %s", verb.Name, s)
				return nil, nil, false
			}
		}
	default:
		b.errorf(pos(v), "", "unexpected value for %s", verb.Name)
		return nil, nil, false
	}
	// Flags first-class: check them against what the command documents.
	if verb.flags != nil {
		for _, f := range flags {
			if !verb.flags[f.name] {
				known := make([]string, 0, len(verb.flags))
				for n := range verb.flags {
					known = append(known, n)
				}
				hint := ""
				if s := closest(f.name, known); s != "" {
					hint = "did you mean --" + s + "?"
				}
				b.errorf(f.at, hint, "%s has no --%s", verb.Name, f.name)
				return nil, nil, false
			}
		}
	}
	return words, flags, true
}

func flagArgs(flags []batchFlag) []string {
	var out []string
	for _, f := range flags {
		if f.hasValue {
			out = append(out, "--"+f.name+"="+f.value)
		} else {
			out = append(out, "--"+f.name)
		}
	}
	return out
}

func isInt(s string) bool { _, err := strconv.Atoi(s); return err == nil }

// build turns a verb and its words into a runnable step.
func (b *batchChecker) build(verb *batchVerb, k *yaml.Node, words []string, flags []batchFlag) *batchStep {
	at := pos(k)
	st := &batchStep{At: at, Verb: verb.Name}
	need := func(n int, what string) bool {
		if len(words) < n {
			b.errorf(at, "usage: "+verb.Usage, "%s needs %s", verb.Name, what)
			return false
		}
		return true
	}
	fl := flagArgs(flags)
	switch verb.Name {
	case "click":
		if len(words) == 2 && isInt(words[0]) && isInt(words[1]) {
			st.Argv = append([]string{"mouse", "click", words[0], words[1]}, fl...)
			break
		}
		if !need(2, "an app and the text to click (click: Calculator Seven)") {
			return nil
		}
		st.Argv = append([]string{"mouse", "click", "--app=" + words[0], "--find=" + strings.Join(words[1:], " ")}, fl...)
	case "type":
		if !need(1, "text to type") {
			return nil
		}
		if len(words) == 1 {
			st.Argv = append([]string{"key", "type", words[0]}, fl...)
		} else {
			st.Argv = append([]string{"key", "type", strings.Join(words[1:], " "), "--app=" + words[0]}, fl...)
		}
	case "key":
		if !need(1, "a key combination") {
			return nil
		}
		if len(words) > 2 {
			b.errorf(at, "key: Notepad ctrl+s", "key takes an app and one combination")
			return nil
		}
		st.Argv = []string{"key", "press", words[len(words)-1]}
		if len(words) == 2 {
			st.Argv = append(st.Argv, "--app="+words[0])
		}
		st.Argv = append(st.Argv, fl...)
	case "screenshot":
		if len(words) > 1 {
			b.errorf(at, "screenshot: Calculator --out=calc.png", "screenshot takes one app")
			return nil
		}
		if len(words) == 1 {
			st.Argv = append([]string{"capture", "screenshot", "window", "--app=" + words[0]}, fl...)
		} else {
			st.Argv = append([]string{"capture", "screenshot"}, fl...)
		}
	case "say":
		if !need(1, "text to say") {
			return nil
		}
		// the file's subtitle: style first, then the step's own flags win
		var style []batchFlag
		for _, d := range b.caption {
			if !slices.ContainsFunc(flags, func(f batchFlag) bool { return f.name == d.name }) {
				style = append(style, d)
			}
		}
		st.Say = append(append([]string{strings.Join(words, " ")}, flagArgs(style)...), fl...)
		for _, f := range flags {
			if f.name == "voice" {
				if err := checkVoice(f.value); err != nil {
					b.errorf(f.at, "see `aos say voices`", "%s", strings.TrimSuffix(err.Error(), "; list them with `aos say voices`"))
					return nil
				}
			}
		}
	case "wait":
		if len(words) != 1 {
			b.errorf(at, "wait: 2s", "wait takes one duration")
			return nil
		}
		d, err := batchDuration(words[0])
		if err != nil {
			b.errorf(at, "500 (ms), 2s, 1m", "%v", err)
			return nil
		}
		st.Wait = d
	case "do":
		if len(words) > 0 && (words[0] == "aos" || words[0] == "aos.exe") {
			words = words[1:]
		}
		if len(words) == 0 {
			b.errorf(at, "do: window snap Notepad left", "do needs an aos command")
			return nil
		}
		cmd, _, err := b.reg.Resolve(words)
		if err != nil || cmd == nil {
			b.errorf(at, "see `aos --help`", "`aos %s` is not an aos command", strings.Join(words, " "))
			return nil
		}
		if batchNoVerb[cmd.Group] && cmd.Group == "batch" {
			b.errorf(at, "", "a batch cannot run another batch")
			return nil
		}
		if batchSystemGroups[cmd.Group] {
			b.warnf(at, "`%s` is a system-level command (%s); it runs only because it is under do:", cmd.Route(), cmd.Group)
		}
		st.Argv = words
	default: // <group>-<command>
		st.Argv = append(append(append([]string{}, verb.Route...), words...), fl...)
	}
	return st
}

func batchDuration(s string) (time.Duration, error) {
	if n, err := strconv.ParseFloat(s, 64); err == nil && n >= 0 {
		return time.Duration(n * float64(time.Millisecond)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%q is not a duration", s)
	}
	return d, nil
}

// splitWords splits a short string into words, honouring "double" and
// 'single' quotes.
func splitWords(s string) ([]string, error) {
	var out []string
	var b strings.Builder
	in, q := false, rune(0)
	for _, r := range s {
		switch {
		case q != 0:
			if r == q {
				q = 0
			} else {
				b.WriteRune(r)
			}
		case r == '"' || r == '\'':
			q, in = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if in {
				out = append(out, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteRune(r)
			in = true
		}
	}
	if q != 0 {
		return nil, fmt.Errorf("unclosed %c quote in %q", q, s)
	}
	if in {
		out = append(out, b.String())
	}
	return out, nil
}

// closest is the nearest name within a small edit distance, or "".
func closest(name string, known []string) string {
	sort.Strings(known)
	best, bestD := "", 3
	for _, k := range known {
		d := editDistance(strings.ToLower(name), strings.ToLower(k))
		if d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// ---- running ----------------------------------------------------------------

type batchResult struct {
	Step   string `json:"step"`
	Run    string `json:"run"`
	OK     bool   `json:"ok"`
	Exit   int    `json:"exit"`
	MS     int64  `json:"ms"`
	Output string `json:"output,omitempty"`
}

func readBatchSource(c *cli.Ctx, arg string) ([]byte, error) {
	if arg == "-" {
		return io.ReadAll(c.Stdin)
	}
	if st, err := os.Stat(expandHome(arg)); err == nil && !st.IsDir() {
		return os.ReadFile(expandHome(arg))
	}
	if strings.ContainsAny(arg, ":[\n") { // inline YAML
		return []byte(arg), nil
	}
	return nil, fmt.Errorf("%s: no such file (inline YAML looks like '[launch: calc, wait: 1s]')", arg)
}

func runBatch(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("check", "json"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("give one file, - for stdin, or the YAML itself: aos batch demo.yaml")
	}
	src, err := readBatchSource(c, set.Rest[0])
	if err != nil {
		return err
	}
	steps, findings := checkBatch(c.Registry, src)
	errs := 0
	for _, f := range findings {
		if f.Severity == "error" {
			errs++
		}
	}
	if set.Has("check") {
		if set.Has("json") {
			if findings == nil {
				findings = []batchFinding{}
			}
			if err := json.NewEncoder(c.Stdout).Encode(findings); err != nil {
				return err
			}
		} else {
			for _, f := range findings {
				c.Println(f.String())
			}
			if errs == 0 {
				c.Printf("ok: %d step(s)\n", len(steps))
			}
		}
		if errs > 0 {
			return &cli.ExitError{Code: 2}
		}
		return nil
	}
	for _, f := range findings {
		fmt.Fprintln(c.Stderr, f.String())
	}
	if errs > 0 {
		return &cli.ExitError{Code: 2, Message: fmt.Sprintf("%d error(s); nothing was run", errs)}
	}

	r := &batchRunner{c: c, json: set.Has("json")}
	start := time.Now()
	ok := true
	for i, st := range steps {
		if !r.run(strconv.Itoa(i+1), st) {
			ok = false
			break
		}
	}
	if r.json {
		_ = json.NewEncoder(c.Stdout).Encode(map[string]any{"summary": true, "ok": ok, "steps": len(steps), "ran": r.ran, "ms": time.Since(start).Milliseconds()})
	} else {
		c.Printf("%s %d step(s) in %.1fs\n", map[bool]string{true: "done:", false: "stopped:"}[ok], r.ran, time.Since(start).Seconds())
	}
	if !ok {
		return &cli.ExitError{Code: 1}
	}
	return nil
}

type batchRunner struct {
	c    *cli.Ctx
	json bool
	mu   sync.Mutex
	ran  int
}

func (r *batchRunner) run(id string, st *batchStep) bool {
	if st.Parallel == nil {
		return r.leaf(id, st)
	}
	var wg sync.WaitGroup
	oks := make([]bool, len(st.Parallel))
	for i, p := range st.Parallel {
		wg.Add(1)
		go func(i int, p *batchStep) {
			defer wg.Done()
			oks[i] = r.leaf(fmt.Sprintf("%s.%d", id, i+1), p)
		}(i, p)
	}
	wg.Wait()
	for _, ok := range oks {
		if !ok {
			return false
		}
	}
	return true
}

func (r *batchRunner) leaf(id string, st *batchStep) bool {
	start := time.Now()
	res := batchResult{Step: id, Run: st.String(), OK: true}
	switch {
	case st.Verb == "wait":
		time.Sleep(st.Wait)
	case st.Verb == "say":
		// A child process, like `say --async`: the speaker queue keys on
		// the process, so parallel lines take turns.
		cmd, err := startSpeech(st.Say, false)
		if err == nil {
			err = cmd.Wait()
		}
		if err != nil {
			res.OK, res.Exit, res.Output = false, 1, err.Error()
		}
	default:
		out := cli.Invoke(r.c, st.Argv, "")
		res.Exit, res.OK = out.Exit, out.Exit == 0
		res.Output = strings.TrimSpace(out.Stdout + out.Stderr)
	}
	res.MS = time.Since(start).Milliseconds()
	r.report(res)
	return res.OK
}

func (r *batchRunner) report(res batchResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ran++
	if r.json {
		_ = json.NewEncoder(r.c.Stdout).Encode(res)
		return
	}
	mark := "ok  "
	if !res.OK {
		mark = "FAIL"
	}
	r.c.Printf("%s %-5s %-50s %5dms\n", mark, res.Step, res.Run, res.MS)
	if !res.OK && res.Output != "" {
		r.c.Printf("     %s\n", strings.ReplaceAll(res.Output, "\n", "\n     "))
	}
}
