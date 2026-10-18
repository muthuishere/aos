//go:build darwin

// web.go — the web content of a browser window, for `aos tab read/type/click`
// and `aos watch tab`.
//
// Only the selected tab of a window has an AXWebArea. Safari exposes it by
// default; Chromium browsers expose nothing below the toolbar until a client
// sets AXEnhancedUserInterface on the application element. That set call
// returns an error on current Chrome (-25208) yet the tree appears anyway, so
// the error is ignored and success is judged by the web area showing up.
// WebPrepare sets it only when the web area is missing and the attribute was
// not already on, only on this browser's process, and hands back a restore
// that turns it off again (it can slow window animations in that app).
package darwin

/*
#cgo CFLAGS: -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework CoreFoundation -framework ApplicationServices

#include <CoreFoundation/CoreFoundation.h>
#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

extern AXError _AXUIElementGetWindow(AXUIElementRef, CGWindowID*);

typedef struct {
    int    depth;
    char*  role;
    char*  subrole;
    char*  title;
    char*  desc;
    char*  value;
    char*  placeholder;
    char*  domid;
    int    level;
    int    focused;
    double x, y, w, h;
} wctl_web_node;

static char* wctl_web_dup(CFTypeRef v) {
    if (!v || CFGetTypeID(v) != CFStringGetTypeID()) return NULL;
    CFStringRef s = (CFStringRef)v;
    if (CFStringGetLength(s) == 0) return NULL;
    CFIndex max = CFStringGetMaximumSizeForEncoding(CFStringGetLength(s), kCFStringEncodingUTF8) + 1;
    char* buf = (char*)malloc(max);
    if (!buf) return NULL;
    if (!CFStringGetCString(s, buf, max, kCFStringEncodingUTF8)) { free(buf); return NULL; }
    return buf;
}

static int wctl_web_is_role(AXUIElementRef el, const char* want) {
    CFTypeRef v = NULL;
    int ok = 0;
    if (AXUIElementCopyAttributeValue(el, kAXRoleAttribute, &v) == kAXErrorSuccess && v) {
        char* r = wctl_web_dup(v);
        ok = r && strcmp(r, want) == 0;
        free(r);
        CFRelease(v);
    }
    return ok;
}

// wctl_web_find returns the first AXWebArea under el (retained), or NULL.
static AXUIElementRef wctl_web_find(AXUIElementRef el, int depth, int* budget) {
    if (depth > 40 || (*budget)-- <= 0) return NULL;
    if (wctl_web_is_role(el, "AXWebArea")) { CFRetain(el); return el; }
    CFArrayRef kids = NULL;
    AXUIElementRef hit = NULL;
    if (AXUIElementCopyAttributeValue(el, kAXChildrenAttribute, (CFTypeRef*)&kids) == kAXErrorSuccess && kids) {
        CFIndex n = CFArrayGetCount(kids);
        for (CFIndex i = 0; i < n && !hit; i++)
            hit = wctl_web_find((AXUIElementRef)CFArrayGetValueAtIndex(kids, i), depth + 1, budget);
        CFRelease(kids);
    }
    return hit;
}

// wctl_web_window finds the app's window by CoreGraphics id, or, when wid
// is 0 (a window on another Space is not in the on-screen list), by its
// exact title when exactly one window has it.
static AXUIElementRef wctl_web_window(AXUIElementRef app, long wid, const char* title) {
    CFArrayRef ws = NULL;
    AXUIElementRef hit = NULL;
    int titled = 0;
    if (AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, (CFTypeRef*)&ws) != kAXErrorSuccess || !ws) return NULL;
    for (CFIndex i = 0; i < CFArrayGetCount(ws); i++) {
        AXUIElementRef w = (AXUIElementRef)CFArrayGetValueAtIndex(ws, i);
        if (wid > 0) {
            CGWindowID id = 0;
            if (_AXUIElementGetWindow(w, &id) == kAXErrorSuccess && (long)id == wid) { CFRetain(w); hit = w; break; }
            continue;
        }
        CFTypeRef t = NULL;
        if (title && *title && AXUIElementCopyAttributeValue(w, kAXTitleAttribute, &t) == kAXErrorSuccess && t) {
            char* s = wctl_web_dup(t);
            size_t n = strlen(title);
            // Chromium titles its window "<page> - Google Chrome".
            if (s && strncmp(s, title, n) == 0 && (s[n] == 0 || strncmp(s + n, " - ", 3) == 0)) {
                titled++;
                if (!hit) { CFRetain(w); hit = w; }
            }
            free(s);
            CFRelease(t);
        }
    }
    CFRelease(ws);
    if (titled > 1) { CFRelease(hit); hit = NULL; }
    return hit;
}

typedef struct {
    wctl_web_node* items;
    int n, cap, max_nodes, max_depth;
    int act_index, action, act_rc;
    AXUIElementRef app;
    AXUIElementRef held; // the focused target, retained for later checks
    const char* want_role;
    const char* want_label;
    CFArrayRef attrs;
} wctl_web_acc;

// Attribute order for the batched read; indexes below must match.
enum { A_ROLE, A_SUBROLE, A_TITLE, A_DESC, A_VALUE, A_PLACEHOLDER, A_FOCUSED, A_POS, A_SIZE, A_DOMID, A_KIDS, A_COUNT };

static void wctl_web_walk(AXUIElementRef el, int depth, wctl_web_acc* acc) {
    if (acc->n >= acc->max_nodes || depth > acc->max_depth) return;
    if (acc->act_index >= 0 && acc->act_rc != 1) return; // action done, stop
    CFArrayRef vals = NULL;
    if (AXUIElementCopyMultipleAttributeValues(el, acc->attrs, 0, &vals) != kAXErrorSuccess || !vals) return;
    if (acc->n == acc->cap) {
        int cap = acc->cap ? acc->cap * 2 : 256;
        wctl_web_node* grown = (wctl_web_node*)realloc(acc->items, sizeof(wctl_web_node) * cap);
        if (!grown) { CFRelease(vals); acc->max_nodes = acc->n; return; } // out of memory: stop, keep what was read
        acc->items = grown; acc->cap = cap;
    }
    int idx = acc->n++;
    wctl_web_node* t = &acc->items[idx];
    memset(t, 0, sizeof(*t));
    t->depth = depth;
    t->role = wctl_web_dup(CFArrayGetValueAtIndex(vals, A_ROLE));
    t->subrole = wctl_web_dup(CFArrayGetValueAtIndex(vals, A_SUBROLE));
    t->title = wctl_web_dup(CFArrayGetValueAtIndex(vals, A_TITLE));
    t->desc = wctl_web_dup(CFArrayGetValueAtIndex(vals, A_DESC));
    t->value = wctl_web_dup(CFArrayGetValueAtIndex(vals, A_VALUE));
    t->placeholder = wctl_web_dup(CFArrayGetValueAtIndex(vals, A_PLACEHOLDER));
    t->domid = wctl_web_dup(CFArrayGetValueAtIndex(vals, A_DOMID));
    // A heading's level is its AXValue (Chromium, WebKit).
    CFTypeRef lv = CFArrayGetValueAtIndex(vals, A_VALUE);
    if (lv && CFGetTypeID(lv) == CFNumberGetTypeID() && t->role && strcmp(t->role, "AXHeading") == 0)
        CFNumberGetValue((CFNumberRef)lv, kCFNumberIntType, &t->level);
    CFTypeRef fv = CFArrayGetValueAtIndex(vals, A_FOCUSED);
    t->focused = fv && CFGetTypeID(fv) == CFBooleanGetTypeID() && CFBooleanGetValue((CFBooleanRef)fv);
    CFTypeRef pv = CFArrayGetValueAtIndex(vals, A_POS), sv = CFArrayGetValueAtIndex(vals, A_SIZE);
    CGPoint p; CGSize sz;
    if (pv && sv && CFGetTypeID(pv) == AXValueGetTypeID() && CFGetTypeID(sv) == AXValueGetTypeID() &&
        AXValueGetValue((AXValueRef)pv, kAXValueCGPointType, &p) && AXValueGetValue((AXValueRef)sv, kAXValueCGSizeType, &sz)) {
        t->x = p.x; t->y = p.y; t->w = sz.width; t->h = sz.height;
    }
    if (idx == acc->act_index) {
        // The fingerprint: the same role, and the same first non-empty of
        // title, description, placeholder (never the value, which typing changes).
        const char* label = t->title ? t->title : t->desc ? t->desc : t->placeholder ? t->placeholder : "";
        if (!t->role || strcmp(t->role, acc->want_role) != 0 || strcmp(label, acc->want_label) != 0) {
            acc->act_rc = 2;
            CFRelease(vals);
            return;
        }
        if (acc->action == 2) {
            // Focus, then prove it: the app's focused element must be this
            // element. No press fallback: a press is not focus.
            acc->act_rc = 3;
            if (AXUIElementSetAttributeValue(el, kAXFocusedAttribute, kCFBooleanTrue) == kAXErrorSuccess) {
                for (int k = 0; k < 10 && acc->act_rc == 3; k++) {
                    CFTypeRef f = NULL;
                    if (AXUIElementCopyAttributeValue(acc->app, kAXFocusedUIElementAttribute, &f) == kAXErrorSuccess && f) {
                        if (CFEqual(f, el)) { acc->act_rc = 0; CFRetain(el); acc->held = el; }
                        CFRelease(f);
                    }
                    if (acc->act_rc == 3) usleep(30000);
                }
            }
        } else {
            acc->act_rc = AXUIElementPerformAction(el, kAXPressAction) == kAXErrorSuccess ? 0 : -1;
        }
        CFRelease(vals);
        return;
    }
    // Static text's children are inline text boxes repeating it.
    int leaf = t->role && (strcmp(t->role, "AXStaticText") == 0 || strcmp(t->role, "AXImage") == 0);
    CFTypeRef kids = CFArrayGetValueAtIndex(vals, A_KIDS);
    if (!leaf && kids && CFGetTypeID(kids) == CFArrayGetTypeID()) {
        CFIndex n = CFArrayGetCount((CFArrayRef)kids);
        for (CFIndex i = 0; i < n; i++)
            wctl_web_walk((AXUIElementRef)CFArrayGetValueAtIndex((CFArrayRef)kids, i), depth + 1, acc);
    }
    CFRelease(vals);
}

// wctl_web_tree walks the web area of window wid of process pid.
// Returns the node count, or -1 not trusted, -2 no window, -3 no web area.
// act_index >= 0 performs action on that preorder node; *act_rc is 0 on
// success, -1 when the action failed, 1 when the node was not reached, 2 when
// the node there no longer has want_role and want_label (nothing is done), 3
// when focus was asked for and the app's focused element is not the node.
static int wctl_web_tree(int pid, long wid, int max_nodes, int max_depth, int act_index, int action,
                         const char* want_role, const char* want_label, const char* title,
                         wctl_web_node** out, int* act_rc, long* found_wid, void** held) {
    *out = NULL; *act_rc = 1; *found_wid = 0; *held = NULL;
    if (!AXIsProcessTrusted()) return -1;
    AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
    if (!app) return -2;
    AXUIElementSetMessagingTimeout(app, 1.0);
    AXUIElementRef win = wctl_web_window(app, wid, title);
    if (!win) { CFRelease(app); return -2; }
    CGWindowID fw = 0;
    if (_AXUIElementGetWindow(win, &fw) == kAXErrorSuccess) *found_wid = (long)fw;
    int budget = 4000;
    AXUIElementRef web = wctl_web_find(win, 0, &budget);
    CFRelease(win);
    if (!web) { CFRelease(app); return -3; }
    CFStringRef names[A_COUNT] = {
        kAXRoleAttribute, kAXSubroleAttribute, kAXTitleAttribute, kAXDescriptionAttribute,
        kAXValueAttribute, CFSTR("AXPlaceholderValue"), kAXFocusedAttribute,
        kAXPositionAttribute, kAXSizeAttribute, CFSTR("AXDOMIdentifier"), kAXChildrenAttribute,
    };
    wctl_web_acc acc = {0};
    acc.max_nodes = max_nodes; acc.max_depth = max_depth;
    acc.act_index = act_index; acc.action = action; acc.act_rc = 1;
    acc.want_role = want_role ? want_role : ""; acc.want_label = want_label ? want_label : "";
    acc.app = app;
    acc.attrs = CFArrayCreate(NULL, (const void**)names, A_COUNT, &kCFTypeArrayCallBacks);
    CFArrayRef kids = NULL;
    if (AXUIElementCopyAttributeValue(web, kAXChildrenAttribute, (CFTypeRef*)&kids) == kAXErrorSuccess && kids) {
        for (CFIndex i = 0; i < CFArrayGetCount(kids); i++)
            wctl_web_walk((AXUIElementRef)CFArrayGetValueAtIndex(kids, i), 0, &acc);
        CFRelease(kids);
    }
    CFRelease(acc.attrs);
    CFRelease(web);
    CFRelease(app);
    *out = acc.items;
    *act_rc = acc.act_rc;
    *held = (void*)acc.held;
    return acc.n;
}

// wctl_web_prepare makes the window's web area visible to accessibility.
// When it is missing and AXEnhancedUserInterface is not already on, it turns
// it on and waits up to ~1.5 s; *enabled says whether this call turned it on
// (so the caller restores it). Returns 0 when the web area is there, -1 not
// trusted, -2 no window, -3 still no web area.
static int wctl_web_prepare(int pid, long wid, const char* title, int* enabled) {
    *enabled = 0;
    if (!AXIsProcessTrusted()) return -1;
    AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
    if (!app) return -2;
    AXUIElementSetMessagingTimeout(app, 1.0);
    AXUIElementRef win = wctl_web_window(app, wid, title);
    if (!win) { CFRelease(app); return -2; }
    int budget = 4000;
    AXUIElementRef web = wctl_web_find(win, 0, &budget);
    if (!web) {
        CFTypeRef prev = NULL;
        int on = AXUIElementCopyAttributeValue(app, CFSTR("AXEnhancedUserInterface"), &prev) == kAXErrorSuccess &&
                 prev && CFGetTypeID(prev) == CFBooleanGetTypeID() && CFBooleanGetValue((CFBooleanRef)prev);
        if (prev) CFRelease(prev);
        if (!on) {
            AXUIElementSetAttributeValue(app, CFSTR("AXEnhancedUserInterface"), kCFBooleanTrue); // error ignored, see above
            *enabled = 1;
        }
        for (int i = 0; i < 10 && !web; i++) {
            usleep(150000);
            budget = 4000;
            web = wctl_web_find(win, 0, &budget);
        }
    }
    CFRelease(win);
    CFRelease(app);
    if (!web) return -3;
    CFRelease(web);
    return 0;
}

// wctl_web_is_focused: 1 when app pid's focused element is el.
static int wctl_web_is_focused(int pid, void* el) {
    AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
    if (!app) return 0;
    AXUIElementSetMessagingTimeout(app, 1.0);
    CFTypeRef f = NULL;
    int same = 0;
    if (AXUIElementCopyAttributeValue(app, kAXFocusedUIElementAttribute, &f) == kAXErrorSuccess && f) {
        same = CFEqual(f, (CFTypeRef)el) ? 1 : 0;
        CFRelease(f);
    }
    CFRelease(app);
    return same;
}

static void wctl_web_release(void* el) { if (el) CFRelease((CFTypeRef)el); }

static void wctl_web_enhance_off(int pid) {
    AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
    if (!app) return;
    AXUIElementSetMessagingTimeout(app, 1.0);
    AXUIElementSetAttributeValue(app, CFSTR("AXEnhancedUserInterface"), kCFBooleanFalse);
    CFRelease(app);
}

static void wctl_web_free(wctl_web_node* t, int n) {
    for (int i = 0; i < n; i++) {
        free(t[i].role); free(t[i].subrole); free(t[i].title);
        free(t[i].desc); free(t[i].value); free(t[i].placeholder); free(t[i].domid);
    }
    free(t);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
)

func (a *Adapter) webWalk(w core.Window, maxNodes, maxDepth, index, action int, role, label string) ([]core.WebNode, int, string, error) {
	nodes, rc, id, held, err := a.webWalkHeld(w, maxNodes, maxDepth, index, action, role, label)
	C.wctl_web_release(held)
	return nodes, rc, id, err
}

func (a *Adapter) webWalkHeld(w core.Window, maxNodes, maxDepth, index, action int, role, label string) ([]core.WebNode, int, string, unsafe.Pointer, error) {
	var wid int64
	if w.ID != "" {
		id, err := parseCGWindowID(w.ID)
		if err != nil {
			return nil, 1, "", nil, err
		}
		wid = int64(id)
	}
	cr, cl, ct := C.CString(role), C.CString(label), C.CString(w.Title)
	defer C.free(unsafe.Pointer(cr))
	defer C.free(unsafe.Pointer(cl))
	defer C.free(unsafe.Pointer(ct))
	var raw *C.wctl_web_node
	var rc C.int
	var found C.long
	var held unsafe.Pointer
	n := int(C.wctl_web_tree(C.int(w.PID), C.long(wid), C.int(maxNodes), C.int(maxDepth), C.int(index), C.int(action), cr, cl, ct, &raw, &rc, &found, &held))
	id := ""
	if found > 0 {
		id = fmt.Sprintf("%d", int64(found))
	}
	switch n {
	case -1:
		return nil, 1, "", nil, core.ErrAccessibilityDenied
	case -2:
		return nil, 1, "", nil, fmt.Errorf("%w: window %s of %s is not in the accessibility tree", core.ErrNoMatch, w.ID, w.App)
	case -3:
		return nil, 1, "", nil, errNoWebArea(w)
	}
	if raw == nil {
		return nil, int(rc), id, held, nil
	}
	defer C.wctl_web_free(raw, C.int(n))
	out := make([]core.WebNode, 0, n)
	for _, t := range unsafe.Slice(raw, n) {
		out = append(out, core.WebNode{
			Depth: int(t.depth), Role: cStringOrEmpty(t.role), Subrole: cStringOrEmpty(t.subrole),
			Title: cStringOrEmpty(t.title), Desc: cStringOrEmpty(t.desc), Value: cStringOrEmpty(t.value),
			Placeholder: cStringOrEmpty(t.placeholder), DOMID: cStringOrEmpty(t.domid), Level: int(t.level), Focused: t.focused != 0,
			Bounds: core.Rect{X: int(t.x), Y: int(t.y), W: int(t.w + 0.5), H: int(t.h + 0.5)},
		})
	}
	return out, int(rc), id, held, nil
}

// WebTree reads the selected tab's web area of window w, and returns the
// window's CoreGraphics id (found through accessibility when w has none).
func (a *Adapter) WebTree(w core.Window, maxNodes, maxDepth int) ([]core.WebNode, string, error) {
	nodes, _, id, err := a.webWalk(w, maxNodes, maxDepth, -1, 0, "", "")
	return nodes, id, err
}

// WebAct performs action on the node at preorder index, but only when it
// still has the expected role and label (core.ErrWebStale otherwise).
func (a *Adapter) WebAct(w core.Window, maxNodes, maxDepth, index, action int, role, label string) error {
	_, rc, _, err := a.webWalk(w, maxNodes, maxDepth, index, action, role, label)
	if err != nil {
		return err
	}
	switch rc {
	case 0:
		return nil
	case 1, 2:
		return core.ErrWebStale
	case 3:
		return core.ErrWebFocus
	}
	return errors.New("the element refused the accessibility action")
}

func errNoWebArea(w core.Window) error {
	return errors.New(w.App + " shows no web content to accessibility for this window (is the tab still loading, or a browser page like settings?)")
}

func (a *Adapter) cWindow(w core.Window) (C.long, *C.char, error) {
	var wid int64
	if w.ID != "" {
		id, err := parseCGWindowID(w.ID)
		if err != nil {
			return 0, nil, err
		}
		wid = id
	}
	return C.long(wid), C.CString(w.Title), nil
}

// WebPrepare makes window w's page visible to accessibility, turning on
// AXEnhancedUserInterface for the browser process when the page is missing.
// restore turns it off again when this call turned it on; call it when done.
func (a *Adapter) WebPrepare(w core.Window) (func(), error) {
	wid, ct, err := a.cWindow(w)
	if err != nil {
		return func() {}, err
	}
	defer C.free(unsafe.Pointer(ct))
	var enabled C.int
	rc := int(C.wctl_web_prepare(C.int(w.PID), wid, ct, &enabled))
	restore := func() {}
	if enabled != 0 {
		pid := C.int(w.PID)
		restore = func() { C.wctl_web_enhance_off(pid) }
	}
	switch rc {
	case 0:
		return restore, nil
	case -1:
		return restore, core.ErrAccessibilityDenied
	case -2:
		return restore, fmt.Errorf("%w: window %s of %s is not in the accessibility tree", core.ErrNoMatch, w.ID, w.App)
	}
	return restore, errNoWebArea(w)
}

type darwinTarget struct {
	pid int
	el  unsafe.Pointer
}

func (t *darwinTarget) StillFocused() (bool, error) {
	if t.el == nil {
		return false, nil
	}
	return C.wctl_web_is_focused(C.int(t.pid), t.el) == 1, nil
}

func (t *darwinTarget) Release() {
	C.wctl_web_release(t.el)
	t.el = nil
}

// WebFocus focuses the node at index (only if it still has role and label),
// proves the app's focused element is that node, and returns a handle that
// re-checks that identity (CFEqual) later.
func (a *Adapter) WebFocus(w core.Window, maxNodes, maxDepth, index int, role, label string) (core.WebTarget, error) {
	_, rc, _, held, err := a.webWalkHeld(w, maxNodes, maxDepth, index, core.WebFocus, role, label)
	if err != nil {
		C.wctl_web_release(held)
		return nil, err
	}
	switch rc {
	case 0:
		return &darwinTarget{pid: w.PID, el: held}, nil
	case 1, 2:
		return nil, core.ErrWebStale
	}
	C.wctl_web_release(held)
	return nil, core.ErrWebFocus
}
