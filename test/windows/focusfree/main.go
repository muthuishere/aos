//go:build windows

// Command focusfree is the focus-free input check for aos on Windows. Put
// aos.exe and testform.exe next to it and run it in a desktop session
// (focusfree.cmd). It puts an Explorer window in front (window A, standing in
// for the person's work), opens each target app behind it, drives the app
// through aos, and after every step checks that the foreground window and the
// pointer did not change and that the effect landed. Full-screen captures are
// saved before and after each step. It closes only what it opened and skips an
// app that was already running, so it never touches a window you are using.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	h "github.com/muthuishere/aos/test/windows/harness"
)

func textOf(sel ...string) string {
	out, _ := h.Aos(append(append([]string{"window", "text"}, sel...), "--source=accessibility")...)
	return out
}

func main() {
	h.Setup("focusfree")
	s := &h.Suite{Settle: 1200 * time.Millisecond, CheckPointer: true}
	tag := h.Tag("aoswt")
	dir := filepath.Join(os.TempDir(), tag)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, tag+".txt"), []byte("start"), 0o644)

	// Window A: an Explorer window on our temp folder, kept in front.
	h.Start("explorer.exe", dir)
	time.Sleep(2 * time.Second)
	var front uintptr
	opened := map[string]bool{}

	term, con, form := "term-"+tag, "con-"+tag, "form-"+tag
	if _, err := exec.LookPath("wt.exe"); err == nil {
		h.Start("wt.exe", "-w", "new", "--title", term, "--suppressApplicationTitle", "cmd", "/k", "echo ready")
		opened["wt"] = true
	} else {
		fmt.Println("SKIP Windows Terminal: not installed")
	}
	conProc := h.Start("conhost.exe", "cmd.exe", "/k", "title "+con)
	formProc := h.Start(filepath.Join(h.Dir, "testform.exe"), form)
	h.Start("notepad.exe", filepath.Join(dir, tag+".txt"))
	opened["notepad"] = true
	if !h.Running("CalculatorApp.exe") {
		h.Start("cmd", "/c", "start", "", "calc.exe")
		opened["calc"] = true
	} else {
		fmt.Println("SKIP calculator: already running")
	}
	if len(h.Find("Settings", "")) == 0 {
		h.Start("cmd", "/c", "start", "", "ms-settings:about")
		opened["settings"] = true
	} else {
		fmt.Println("SKIP settings: a Settings window is open")
	}
	h.Start("cmd", "/c", "start", "", "msedge.exe", "--inprivate", "--new-window", "about:blank")
	opened["edge"] = true
	time.Sleep(6 * time.Second)
	if ws := h.Find("*"+tag+"*", "explorer"); len(ws) > 0 {
		front = ws[0].H()
	}
	h.Front(front)
	fmt.Println("window A (foreground): " + h.Fg())
	list, _ := h.Aos("window", "list")
	fmt.Println(list)

	s.Check("baseline: no aos call (late activations show here)", func() (string, int) { return "idle", 0 }, func(string) bool { return true })
	h.Front(front)

	// --- terminals: console input buffer
	if opened["wt"] {
		s.Check("Windows Terminal key type", h.AosStep("key", "type", "--title="+term, "echo hello-wt-aos", "--json"), h.Has("console input"))
		s.Check("Windows Terminal key press enter", h.AosStep("key", "press", "enter", "--title="+term, "--json"), h.Has(`"Route":"post"`))
		time.Sleep(time.Second)
		s.Check("Windows Terminal window text", h.AosStep("window", "text", "--title="+term), h.Has("hello-wt-aos"))
		h.Front(front)
	}
	s.Check("conhost key type", h.AosStep("key", "type", "--title="+con, "echo hello-con-aos", "--json"), h.Has("console input"))
	s.Check("conhost key press enter", h.AosStep("key", "press", "enter", "--title="+con, "--json"), h.Has(`"Route":"post"`))
	time.Sleep(time.Second)
	s.Check("conhost window text", h.AosStep("window", "text", "--title="+con), h.Has("hello-con-aos"))
	h.Front(front)

	// --- classic Win32 (testform: EDIT + ClickMe button)
	s.Check("Win32 edit key type", h.AosStep("key", "type", "--title="+form, " typed-by-aos", "--json"),
		func(string) bool { return strings.Contains(textOf("--title="+form), "typed-by-aos") })
	s.Check("Win32 button mouse click --find", h.AosStep("mouse", "click", "--title="+form, "--find=ClickMe"),
		func(string) bool { return strings.Contains(textOf("--title="+form), "clicked") })
	s.Check("Win32 window text", h.AosStep("window", "text", "--title="+form), h.Has("typed-by-aos"))
	h.Front(front)
	if opened["notepad"] {
		s.Check("Notepad key type", h.AosStep("key", "type", "--app=Notepad", "--title="+tag, "hello-notepad-aos", "--json"),
			func(string) bool {
				return strings.Contains(textOf("--app=Notepad", "--title="+tag), "hello-notepad-aos")
			})
		s.Check("Notepad window text", h.AosStep("window", "text", "--app=Notepad", "--title="+tag), h.Has("hello-notepad-aos"))
		s.Check("Notepad find", h.AosStep("find", "--app=Notepad", "--title="+tag, "--text=hello-notepad-aos"), h.Has("hello-notepad-aos"))
		h.Front(front)
	}

	// --- UIA apps
	if opened["calc"] {
		s.Check("Calculator find", h.AosStep("find", "--title=Calculator", "--text=Seven", "--role=button"), h.Has("Seven"))
		s.Check("Calculator click 7", h.AosStep("mouse", "click", "--title=Calculator", "--find=Seven"), h.Has("press"))
		s.Check("Calculator click +", h.AosStep("mouse", "click", "--title=Calculator", "--find=Plus"), h.Has("press"))
		s.Check("Calculator click 2", h.AosStep("mouse", "click", "--title=Calculator", "--find=Two"), h.Has("press"))
		s.Check("Calculator click =", h.AosStep("mouse", "click", "--title=Calculator", "--find=Equals"),
			func(string) bool { return strings.Contains(textOf("--title=Calculator"), "Display is 9") })
		s.Check("Calculator key type (no editable field)", h.AosStep("key", "type", "--title=Calculator", "5"), h.Has("focus|editable"))
		h.Front(front)
	}
	if opened["settings"] {
		s.Check("Settings (WinUI) window text", h.AosStep("window", "text", "--title=Settings"), h.Has("About|Device specifications"))
		s.Check("Settings (WinUI) click Open Navigation", h.AosStep("mouse", "click", "--title=Settings", "--find=Open Navigation"), h.Has("press"))
		h.Front(front)
	}
	exp := tag + " - File Explorer"
	s.Check("Explorer find", h.AosStep("find", "--title="+exp, "--text="+tag), h.Has(regexp.QuoteMeta(tag)))
	s.Check("Explorer window text", h.AosStep("window", "text", "--title="+exp), h.Has(regexp.QuoteMeta(tag)))
	if opened["edge"] {
		s.Check("Edge (InPrivate) key type (address bar)", h.AosStep("key", "type", "--title=InPrivate", "aos-edge-test", "--json"),
			func(string) bool { return strings.Contains(textOf("--title=InPrivate"), "aos-edge-test") })
		s.Check("Edge (InPrivate) window text", h.AosStep("window", "text", "--title=InPrivate"), func(o string) bool { return len(o) > 0 })
		h.Front(front)
	}

	// Polite guard: a pointer move to where the pointer already is.
	s.Check("polite guard: mouse move (exit 0 idle / 5 active)", func() (string, int) {
		nums := regexp.MustCompile(`-?\d+`).FindAllString(h.Cursor(), 2)
		if len(nums) < 2 {
			return "no position", 1
		}
		return h.Aos("mouse", "move", nums[0], nums[1])
	}, func(o string) bool { return !regexp.MustCompile(`error|failed`).MatchString(o) })
	obs, _ := h.Aos("obs", "focus", "--limit=4")
	fmt.Println(obs)

	// --- clean up only what we opened (by window handle; nothing else is touched)
	if opened["wt"] {
		h.CloseWindows(term)
	}
	if conProc != nil {
		h.KillTree(conProc.Pid)
	}
	if formProc != nil {
		h.KillTree(formProc.Pid)
	}
	if opened["notepad"] {
		// Win11 Notepad opens our file as a TAB in a window that may hold the
		// owner's unsaved tabs: never close the window. Mark our tab
		// unmodified, then press its own Close Tab button (the active tab is
		// the only one with one).
		for _, w := range h.Find("*"+tag+"*Notepad", "") {
			h.ClearNotepadModified(w.H())
		}
		out, _ := h.Aos("find", "--app=Notepad", "--title="+tag, "--text=Close Tab", "--role=button", "--all", "--json")
		if n := strings.Count(out, `"Center"`); n == 1 {
			h.Aos("mouse", "click", "--app=Notepad", "--title="+tag, "--find=Close Tab", "--role=button")
		} else {
			fmt.Printf("LEFT OPEN: our Notepad tab (%d Close Tab buttons; not guessing)\n", n)
		}
		time.Sleep(time.Second)
	}
	if opened["calc"] {
		_ = exec.Command("taskkill", "/F", "/IM", "CalculatorApp.exe").Run()
	}
	if opened["settings"] {
		h.CloseWindows("Settings")
	}
	if opened["edge"] {
		h.CloseWindows("*InPrivate*")
	}
	h.CloseWindows(exp)
	time.Sleep(2 * time.Second)
	h.Shot("zz-after-cleanup")
	_ = os.RemoveAll(dir)
	os.Exit(s.Finish("shot-results.json"))
}
