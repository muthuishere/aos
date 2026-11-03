//go:build windows

// Command edge checks Edge focus-free: a local test page in a new InPrivate
// window behind an existing window (Notepad if open), driven with aos tab
// list / read / type / click and window text. It checks the foreground after
// every step, saves before/after screenshots, and closes only the InPrivate
// window it opened. Run edge.cmd with aos.exe next to it.
package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	h "github.com/muthuishere/aos/test/windows/harness"
)

func main() {
	h.Setup("edge")
	s := &h.Suite{Name: "edge", Before: 800 * time.Millisecond, Settle: 1500 * time.Millisecond}
	tag := h.Tag("edge")
	dir := filepath.Join(os.TempDir(), tag)
	_ = os.MkdirAll(dir, 0o755)
	page := filepath.Join(dir, tag+".html")
	_ = os.WriteFile(page, []byte(`<!doctype html><html><head><title>`+tag+` test page</title></head><body>
<h1>aos edge test</h1>
<label>Message <input id="msg" aria-label="Message" placeholder="Message"></label>
<button id="send" onclick="document.getElementById('out').textContent='sent: '+document.getElementById('msg').value">Send</button>
<p id="out">nothing sent</p>
<form onsubmit="document.getElementById('sub').textContent='submitted: '+document.getElementById('q').value; return false"><input id="q" aria-label="Search" placeholder="Search"></form>
<p id="sub">not submitted</p>
</body></html>`), 0o644)
	before := len(h.Find("*", "msedge"))
	pageURL := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(page)}).String()
	h.Start("cmd", "/c", "start", "", "msedge.exe", "--inprivate", "--new-window", pageURL)
	time.Sleep(6 * time.Second)
	var mine *h.Window
	if ws := h.Find("*"+tag+"*", "msedge"); len(ws) > 0 {
		mine = &ws[0]
		fmt.Printf("our edge window: %s '%s'  (edge windows before: %d)\n", mine.ID, mine.Title, before)
	}
	front := h.Find("*", "Notepad")
	if len(front) == 0 {
		for _, w := range h.Windows() {
			if !strings.EqualFold(w.App, "msedge") {
				front = []h.Window{w}
				break
			}
		}
	}
	if len(front) > 0 {
		h.Front(front[0].H())
	}
	fmt.Println("front: " + h.Fg())

	// The page's accessibility tree updates a moment after an action: read with retries.
	pageHas := func(want string) bool {
		for i := 0; i < 6; i++ {
			if out, _ := h.Aos("tab", "read", "--title="+tag); strings.Contains(out, want) {
				return true
			}
			time.Sleep(500 * time.Millisecond)
		}
		return false
	}
	s.Check("tab list finds the page", h.AosStep("tab", "list", "--app=msedge", "--json"), h.Has(tag))
	s.Check("tab read (page as markdown with refs)", h.AosStep("tab", "read", "--title="+tag),
		func(o string) bool { return strings.Contains(o, "aos edge test") && strings.Contains(o, "Send") })
	s.Check("tab type into Message field (Notepad in front)", h.AosStep("tab", "type", "--title="+tag, "--into=Message", "--text=hello-edge"),
		func(o string) bool { return strings.Contains(o, "SetValue") && pageHas("hello-edge") })
	s.Check("tab click Send (Notepad in front)", h.AosStep("tab", "click", "--title="+tag, "--find=Send", "--role=button"),
		func(string) bool { return pageHas("sent: hello-edge") })
	if mine != nil {
		s.Check("window text of the edge window", h.AosStep("window", "text", "--id="+mine.ID), h.Has("sent: hello-edge"))
	}
	// A terminal in front instead of Notepad.
	h.Aos("tab", "open", "--app=WindowsTerminal", "--wt-window=aosE-"+tag, "--title=E-"+tag)
	time.Sleep(3 * time.Second)
	term := h.Find("E-"+tag, "")
	if len(term) > 0 {
		h.Front(term[0].H())
	}
	fmt.Println("front: " + h.Fg())
	s.Check("tab type (terminal in front)", h.AosStep("tab", "type", "--title="+tag, "--into=Message", "--text=typed-while-terminal-in-front"), h.Has("SetValue"))
	s.Check("tab type --enter submits (foreground taken for Enter, handed back)",
		h.AosStep("tab", "type", "--title="+tag, "--into=Search", "--text=query-aos", "--enter", "--force"), h.Has("typed into"))
	s.Check("tab click Send (terminal in front)", h.AosStep("tab", "click", "--title="+tag, "--find=Send", "--role=button"), h.Has("."))
	if len(term) > 0 {
		h.Aos("tab", "close", "--title=E-"+tag, "--app=WindowsTerminal")
	}
	time.Sleep(2 * time.Second)
	// Chromium does not update a fully covered window's accessibility tree, so
	// what was typed while the terminal covered it is checked once it is visible.
	s.Check("after the terminal closed: the typing, the click and the submit had landed", func() (string, int) { return "check", 0 },
		func(string) bool {
			return pageHas("sent: typed-while-terminal-in-front") && pageHas("submitted: query-aos")
		})

	// Close only our InPrivate window.
	if mine != nil {
		h.Post(mine.H(), h.WMClose, 0, 0)
	}
	time.Sleep(2 * time.Second)
	_ = os.RemoveAll(dir)
	h.Shot("zz-edge-after-cleanup")
	fmt.Printf("our edge window left: %d; edge windows now: %d\n", len(h.Find("*"+tag+"*", "")), len(h.Find("*", "msedge")))
	os.Exit(s.Finish("shot-edge-results.json"))
}
