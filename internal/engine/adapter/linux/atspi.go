//go:build linux

package linux

// AccessibleTexts on Linux: AT-SPI2 over D-Bus, pure Go (no cgo, no
// libatspi). GTK, Qt, Firefox, Chromium (with accessibility on) and
// LibreOffice publish their widget trees on the accessibility bus.
//
// The accessibility bus is its own D-Bus daemon; its address comes from
// AT_SPI_BUS_ADDRESS or from org.a11y.Bus.GetAddress on the session bus.
// Under Wayland, components report no screen coordinates (extents come
// back relative or zero), so boxes and reading order are best-effort
// there; X11 gives real screen coordinates.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/muthuishere/aos/internal/engine/core"
)

const (
	atspiRegistry   = "org.a11y.atspi.Registry"
	atspiRoot       = "/org/a11y/atspi/accessible/root"
	ifAccessible    = "org.a11y.atspi.Accessible"
	ifComponent     = "org.a11y.atspi.Component"
	ifText          = "org.a11y.atspi.Text"
	atspiMaxNodes   = 8000
	atspiMaxDepth   = 64
	atspiBudget     = 1500 * time.Millisecond
	stateShowing    = 25
	roleFrame       = 23
	rolePassword    = 40
	atspiMaxTextLen = 200000
)

// ErrNoAccessibilityBus: no AT-SPI bus to talk to (no session bus, or
// at-spi2-core is not installed / not running).
var ErrNoAccessibilityBus = errors.New("no AT-SPI accessibility bus (install at-spi2-core and run inside a desktop session)")

type atspiRef struct {
	Name string
	Path dbus.ObjectPath
}

func a11yAddress() (string, error) {
	if addr := os.Getenv("AT_SPI_BUS_ADDRESS"); addr != "" {
		return addr, nil
	}
	sess, err := dbus.ConnectSessionBus()
	if err != nil {
		return "", fmt.Errorf("%w: session bus: %v", ErrNoAccessibilityBus, err)
	}
	defer sess.Close()
	var addr string
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := sess.Object("org.a11y.Bus", "/org/a11y/bus").CallWithContext(ctx, "org.a11y.Bus.GetAddress", 0).Store(&addr); err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoAccessibilityBus, err)
	}
	return addr, nil
}

func dialA11y() (*dbus.Conn, error) {
	addr, err := a11yAddress()
	if err != nil {
		return nil, err
	}
	conn, err := dbus.Dial(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoAccessibilityBus, err)
	}
	if err := conn.Auth(nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: auth: %v", ErrNoAccessibilityBus, err)
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: hello: %v", ErrNoAccessibilityBus, err)
	}
	return conn, nil
}

// AccessibleBusOK reports whether an AT-SPI bus answers (for doctor).
func (a *Adapter) AccessibleBusOK() error {
	conn, err := dialA11y()
	if err != nil {
		return err
	}
	return conn.Close()
}

type atspiWalk struct {
	ctx   context.Context
	conn  *dbus.Conn
	n     int
	items []core.TextItem
}

func (w *atspiWalk) call(r atspiRef, method string, out any, args ...any) error {
	return w.conn.Object(r.Name, r.Path).CallWithContext(w.ctx, method, 0, args...).Store(out)
}

func (w *atspiWalk) prop(r atspiRef, iface, name string) (dbus.Variant, error) {
	var v dbus.Variant
	err := w.conn.Object(r.Name, r.Path).CallWithContext(w.ctx, "org.freedesktop.DBus.Properties.Get", 0, iface, name).Store(&v)
	return v, err
}

func (w *atspiWalk) name(r atspiRef) string {
	v, err := w.prop(r, ifAccessible, "Name")
	if err != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return strings.TrimSpace(s)
}

func (w *atspiWalk) children(r atspiRef) []atspiRef {
	var kids []atspiRef
	if err := w.call(r, ifAccessible+".GetChildren", &kids); err != nil {
		return nil
	}
	return kids
}

func (w *atspiWalk) extents(r atspiRef) (core.Rect, bool) {
	var e struct{ X, Y, W, H int32 } // one (iiii) struct
	if err := w.call(r, ifComponent+".GetExtents", &e, uint32(0) /* screen */); err != nil {
		return core.Rect{}, false
	}
	x, y, ww, h := e.X, e.Y, e.W, e.H
	return core.Rect{X: int(x), Y: int(y), W: int(ww), H: int(h)}, ww > 0 && h > 0
}

func (w *atspiWalk) showing(r atspiRef) bool {
	var st []uint32
	if err := w.call(r, ifAccessible+".GetState", &st); err != nil || len(st) == 0 {
		return true // unknown: don't hide it
	}
	return st[0]&(1<<stateShowing) != 0
}

func (w *atspiWalk) hasIface(r atspiRef, iface string) bool {
	var ifs []string
	if err := w.call(r, ifAccessible+".GetInterfaces", &ifs); err != nil {
		return false
	}
	for _, i := range ifs {
		if i == iface {
			return true
		}
	}
	return false
}

func (w *atspiWalk) role(r atspiRef) uint32 {
	var role uint32
	_ = w.call(r, ifAccessible+".GetRole", &role)
	return role
}

func (w *atspiWalk) walk(r atspiRef, depth int) {
	if depth > atspiMaxDepth || w.n >= atspiMaxNodes || w.ctx.Err() != nil {
		return
	}
	w.n++
	if !w.showing(r) {
		return
	}
	role := w.role(r)
	name := w.name(r)
	value := ""
	if role != rolePassword && w.hasIface(r, ifText) {
		var s string
		if err := w.call(r, ifText+".GetText", &s, int32(0), int32(-1)); err == nil {
			if len(s) > atspiMaxTextLen {
				s = s[:atspiMaxTextLen]
			}
			value = strings.TrimSpace(strings.ReplaceAll(s, "￼", "")) // embedded-object marks
		}
	}
	if name == "" {
		name, value = value, ""
	} else if value == name {
		value = ""
	}
	if name != "" && depth > 0 {
		if b, ok := w.extents(r); ok {
			w.items = append(w.items, core.TextItem{Text: name, Value: value, Role: atspiRole(role),
				Bounds: b, Source: "accessibility"})
		}
	}
	for _, k := range w.children(r) {
		w.walk(k, depth+1)
	}
}

// AccessibleTexts walks the AT-SPI tree of the frame that is window id:
// the app with the window's PID (any app when the PID is unknown), and in
// it the frame whose name is the window title (else the one whose box
// matches the window).
func (a *Adapter) AccessibleTexts(id string) ([]core.TextItem, error) {
	ws, err := a.ListWindows()
	if err != nil {
		return nil, err
	}
	var win core.Window
	found := false
	for _, w := range ws {
		if w.ID == id {
			win, found = w, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: no window has id %s", core.ErrNoMatch, id)
	}
	conn, err := dialA11y()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), atspiBudget)
	defer cancel()
	wk := &atspiWalk{ctx: ctx, conn: conn}
	frame, ok := wk.findFrame(win)
	if !ok {
		return nil, fmt.Errorf("%w: window %q is not on the AT-SPI bus (the app may not support accessibility; GTK/Qt apps need at-spi2-core, Chromium needs --force-renderer-accessibility)", core.ErrNoMatch, win.Title)
	}
	wk.walk(frame, 0)
	return wk.items, nil
}

func (w *atspiWalk) findFrame(win core.Window) (atspiRef, bool) {
	apps := w.children(atspiRef{atspiRegistry, atspiRoot})
	var best atspiRef
	bestScore := -1
	for _, app := range apps {
		if win.PID > 0 {
			var pid uint32
			if err := w.conn.BusObject().CallWithContext(w.ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, app.Name).Store(&pid); err == nil && int(pid) != win.PID {
				continue
			}
		}
		for _, f := range w.children(app) {
			score := 0
			if n := w.name(f); n != "" && n == win.Title {
				score += 4
			} else if n != "" && win.Title != "" && strings.Contains(win.Title, n) {
				score += 2
			}
			if b, ok := w.extents(f); ok && near(b, win.Bounds) {
				score += 2
			}
			if w.role(f) == roleFrame {
				score++
			}
			if score > bestScore {
				best, bestScore = f, score
			}
		}
	}
	return best, bestScore >= 0
}

func near(a, b core.Rect) bool {
	d := func(x, y int) bool { return x-y < 40 && y-x < 40 }
	return d(a.X, b.X) && d(a.Y, b.Y) && d(a.W, b.W) && d(a.H, b.H)
}

func atspiRole(r uint32) string {
	switch r {
	case 43, 62: // push button, toggle button
		return "button"
	case 33, 34, 35, 8, 45: // menu, menu bar, menu item, check/radio menu item
		return "menuitem"
	case 88:
		return "link"
	case 37:
		return "tab"
	case 7:
		return "checkbox"
	case 44:
		return "radio"
	case 61, 79, 11, 40, 52: // text, entry, combo box, password, spin button
		return "textfield"
	case 29, 73, 83, 60: // label, paragraph, heading, terminal
		return "text"
	case 27, 26:
		return "image"
	}
	return "other"
}
