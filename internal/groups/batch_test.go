package groups

import (
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

func batchRegistry() *cli.Registry {
	r := cli.NewRegistry()
	Register(r)
	return r
}

func TestBatchGoodScripts(t *testing.T) {
	r := batchRegistry()
	cases := []struct {
		src  string
		want []string // each step's String()
	}{
		{`[launch: calc, parallel: [say: seven, click: Calculator Seven], type: Notepad done]`,
			[]string{"launch calc", "parallel [say seven | mouse click --app=Calculator --find=Seven]", "key type done --app=Notepad"}},
		{"steps:\n  - wait: 2s\n  - key: Notepad ctrl+s\n  - key: enter\n  - window-list: --app=Notepad\n",
			[]string{"wait 2s", "key press ctrl+s --app=Notepad", "key press enter", "window list --app=Notepad"}},
		{`[click: {app: Calculator, target: Seven}, click: 10 20, type: hello, display-list]`,
			[]string{"mouse click --app=Calculator --find=Seven", "mouse click 10 20", "key type hello", "display list"}},
		{`[record: demo.mp4 window --app=TextEdit, record-stop: --narration=replace]`,
			[]string{"capture video start window --out=demo.mp4 --app=TextEdit", "capture video stop --narration=replace"}},
		{"steps:\n  - screenshot: Calculator --out=c.png\n  - screenshot\n  - do: aos window focus Notepad\n  - type: Notepad \"a, b\" c\n",
			[]string{"capture screenshot window --app=Calculator --out=c.png", "capture screenshot", "window focus Notepad", "key type a, b c --app=Notepad"}},
	}
	for _, c := range cases {
		steps, fs := checkBatch(r, []byte(c.src))
		for _, f := range fs {
			if f.Severity == "error" {
				t.Errorf("%s: unexpected %s", c.src, f)
			}
		}
		var got []string
		for _, s := range steps {
			got = append(got, s.String())
		}
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", c.src, got, c.want)
		}
	}
}

func TestBatchBadScripts(t *testing.T) {
	r := batchRegistry()
	cases := []struct{ src, want string }{
		{`[lanch: calc]`, `1:2: error: unknown verb "lanch" (did you mean launch?)`},
		{`[click: Calc]`, `1:2: error: click needs an app and the text to click`},
		{`[wait: soon]`, `1:2: error: "soon" is not a duration`},
		{`[system-info]`, `1:2: error: "system-info" changes the system; system-level commands are not batch verbs (use ` + "`do: system info`" + ` if you really mean it)`},
		{`[do: nosuch thing]`, "1:2: error: `aos nosuch thing` is not an aos command"},
		{`[say: hi --voise=Zira]`, `1:7: error: say has no --voise (did you mean --voice?)`},
		{`[window-list: --ap=X]`, `1:15: error: window-list has no --ap (did you mean --app?)`},
		{`[{launch: a, click: b c}]`, `1:14: error: one step has two verbs (launch and click)`},
		{`[parallel: [parallel: [wait: 1]]]`, `1:13: error: parallel inside parallel is not supported`},
		{`[parallel: []]`, `1:2: error: parallel needs at least one step`},
		{`[key: a b c]`, `1:2: error: key takes an app and one combination`},
		{`[record: demo.mov]`, `1:2: error: record needs an .mp4 file name`},
		{`[type: "unclosed]`, `1:1: error: found unexpected end of stream`},
		{"steps: [wait: 1]\nrepeat: 3\n", `2:1: error: unknown top-level key "repeat"`},
		{`[do: batch x.yaml]`, `1:2: error: a batch cannot run another batch`},
	}
	for _, c := range cases {
		_, fs := checkBatch(r, []byte(c.src))
		var all []string
		for _, f := range fs {
			all = append(all, f.String())
		}
		joined := strings.Join(all, "\n")
		if !strings.Contains(joined, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.src, joined, c.want)
		}
	}
}

func TestBatchSystemLevelThroughDoWarns(t *testing.T) {
	_, fs := checkBatch(batchRegistry(), []byte(`[do: system info]`))
	if len(fs) != 1 || fs[0].Severity != "warning" || !strings.Contains(fs[0].Message, "system-level") {
		t.Errorf("got %v", fs)
	}
}

func TestBatchVerbsCoverEveryPublicCommand(t *testing.T) {
	r := batchRegistry()
	verbs := batchVerbs(r)
	for _, c := range r.Commands() {
		if c.Hidden || c.Run == nil || batchNoVerb[c.Group] || batchSystemGroups[c.Group] {
			continue
		}
		name := c.Group
		if c.Name != "" {
			name += "-" + strings.ReplaceAll(c.Name, " ", "-")
		}
		if verbs[name] == nil {
			t.Errorf("no verb for %q", c.Route())
		}
	}
}
