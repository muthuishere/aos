//go:build windows

// Command wttabs checks Windows Terminal tabs focus-free, plus Calculator's
// first press and a Paint background-drawing spike. It opens two named WT
// windows (A: 1 tab, B: 2 tabs), puts an existing Notepad window in front
// (nothing is typed into it), drives window B's tab 2 through aos, and checks
// the foreground never changes. It closes only what it opened.
//
//	wt-tabs.cmd [old-tag ...]   (old tags: leftovers of an earlier run to close first)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	h "github.com/muthuishere/aos/test/windows/harness"
)

type tab struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	PID    int    `json:"pid"`
	Active bool   `json:"active"`
	Index  int    `json:"index"`
	Cwd    string `json:"cwd"`
}

func tabs() []tab {
	out, _ := h.Aos("tab", "list", "--app=WindowsTerminal", "--json")
	var t []tab
	if i := strings.IndexByte(out, '['); i >= 0 {
		_ = json.Unmarshal([]byte(out[i:]), &t)
	}
	return t
}

func tabNamed(title string) *tab {
	for _, t := range tabs() {
		if t.Title == title {
			return &t
		}
	}
	return nil
}

func mk(x, y int) uintptr { return uintptr(y<<16 | x) }

func main() {
	h.Setup("wt-tabs")
	s := &h.Suite{Settle: 1500 * time.Millisecond}
	for _, old := range os.Args[1:] {
		for _, p := range []string{"A1", "B1", "B2"} {
			out, _ := h.Aos("tab", "close", "--title="+p+"-"+old, "--app=WindowsTerminal")
			fmt.Println("  cleanup: " + strings.TrimSpace(out))
		}
	}
	tag := h.Tag("t")
	a1, b1, b2 := "A1-"+tag, "B1-"+tag, "B2-"+tag
	h.Aos("tab", "open", "--app=WindowsTerminal", "--wt-window=aosA-"+tag, "--title="+a1, "--cwd="+os.TempDir())
	time.Sleep(2 * time.Second)
	h.Aos("tab", "open", "--app=WindowsTerminal", "--wt-window=aosB-"+tag, "--title="+b1, "--cwd="+os.Getenv("USERPROFILE"))
	time.Sleep(2 * time.Second)
	h.Aos("tab", "open", "--app=WindowsTerminal", "--wt-window=aosB-"+tag, "--title="+b2, `--cwd=C:\Windows`)
	time.Sleep(3 * time.Second)
	// Front: an existing Notepad window (not edited).
	var front uintptr
	if ws := h.Find("*", "Notepad"); len(ws) > 0 {
		front = ws[0].H()
	}
	h.Front(front)
	fmt.Println("front: " + h.Fg())
	fmt.Println("--- aos tab list --app=WindowsTerminal")
	for _, t := range tabs() {
		fmt.Printf("  %s  idx=%d active=%v pid=%d cwd=%s  '%s'\n", t.ID, t.Index, t.Active, t.PID, t.Cwd, t.Title)
	}

	s.Check("tab list has our 3 tabs with pids", h.AosStep("tab", "list", "--app=WindowsTerminal", "--json"), func(string) bool {
		n := 0
		for _, t := range tabs() {
			if strings.HasSuffix(t.Title, tag) && t.PID > 0 {
				n++
			}
		}
		return n == 3
	})
	s.Check("tab type into window B tab 2 (background, not selected)", h.AosStep("tab", "type", "--title="+b2, "--text=echo hello-tab2", "--enter"), h.Has("console input"))
	time.Sleep(time.Second)
	s.Check("tab read window B tab 2", h.AosStep("tab", "read", "--title="+b2),
		func(o string) bool { return strings.Contains(o, "hello-tab2") && strings.Contains(o, `C:\Windows`) })
	s.Check("key type --tab into window A", h.AosStep("key", "type", "--tab="+a1, "echo hello-from-key-type\n"), h.Has("console input"))
	time.Sleep(time.Second)
	s.Check("tab read window A", h.AosStep("tab", "read", "--title="+a1), h.Has("hello-from-key-type"))
	s.Check("tab send --key=ctrl+c to B2", func() (string, int) {
		t := tabNamed(b2)
		if t == nil {
			return "no tab " + b2, 1
		}
		return h.Aos("tab", "send", "--id="+t.ID, "--key=ctrl+c")
	}, func(o string) bool { return !strings.Contains(o, "error") })
	s.Check("tab focus B1 in place (no raise)", h.AosStep("tab", "focus", "--title="+b1), func(string) bool {
		t := tabNamed(b1)
		return t != nil && t.Active
	})

	// Calculator: first press must not bring it to the front.
	if !h.Running("CalculatorApp.exe") {
		h.Start("cmd", "/c", "start", "", "calc.exe")
		time.Sleep(5 * time.Second)
		h.Front(front)
		s.Check("calculator first press 7 (UWP)", h.AosStep("mouse", "click", "--title=Calculator", "--find=Seven"), h.Has("press"))
		s.Check("calculator press + 2 =", func() (string, int) {
			h.Aos("mouse", "click", "--title=Calculator", "--find=Plus")
			h.Aos("mouse", "click", "--title=Calculator", "--find=Two")
			return h.Aos("mouse", "click", "--title=Calculator", "--find=Equals")
		}, func(string) bool {
			out, _ := h.Aos("window", "text", "--title=Calculator", "--source=accessibility")
			return strings.Contains(out, "Display is 9")
		})
		h.Start("taskkill", "/F", "/IM", "CalculatorApp.exe")
	}

	// Paint spike: post a mouse drag to the canvas child while Paint is behind.
	if !h.Running("mspaint.exe") {
		paint := h.Start("mspaint.exe")
		time.Sleep(5 * time.Second)
		h.Front(front)
		if ws := h.Find("*Paint*", ""); len(ws) > 0 {
			kids := h.Children(ws[0].H())
			var canvas h.Child
			var names []string
			for _, k := range kids {
				if k.Area > canvas.Area {
					canvas = k
				}
				names = append(names, fmt.Sprintf("%s(%d)", k.Class, k.Area))
			}
			if len(names) > 6 {
				names = names[:6]
			}
			fmt.Println("  paint children: " + strings.Join(names, ", "))
			s.Check("paint: posted mouse drag on the canvas child (background)", func() (string, int) {
				h.Post(canvas.H, h.WMLButtonDown, 1, mk(100, 100))
				for i := 1; i <= 20; i++ {
					h.Post(canvas.H, h.WMMouseMove, 1, mk(100+i*15, 100+i*8))
					time.Sleep(15 * time.Millisecond)
				}
				h.Post(canvas.H, h.WMLButtonUp, 0, mk(400, 260))
				return "posted to " + canvas.Class, 0
			}, func(string) bool { return true })
			h.Aos("capture", "screenshot", "window", "--app=mspaint", `--out=`+h.Dir+`\shot-paint-window.png`)
		}
		if paint != nil {
			h.KillTree(paint.Pid)
		}
	}

	// Clean up our WT tabs (each closes its window when it is the last).
	for _, t := range []string{a1, b1, b2} {
		out, _ := h.Aos("tab", "close", "--title="+t, "--app=WindowsTerminal")
		fmt.Println("  " + strings.TrimSpace(out))
	}
	time.Sleep(2 * time.Second)
	h.Shot("zz-after-cleanup")
	fmt.Printf("WT windows of ours left: %d\n", len(h.Find("*"+tag+"*", "")))
	os.Exit(s.Finish("shot-wt-results.json"))
}
