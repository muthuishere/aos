//go:build darwin

// Package darwin is the macOS platform adapter for engine.
//
// ListWindows and ListMonitors are implemented against CoreGraphics; they
// require no special permissions beyond the Screen Recording prompt
// (window owner names / IDs / sizes always come back; titles are
// suppressed by the OS until the prompt is granted).
//
// Move and Focus are wired against the Accessibility (AX) API in
// ApplicationServices. Each call:
//
//  1. Pre-flights AXIsProcessTrustedWithOptions — if false, returns
//     core.ErrAccessibilityDenied immediately. The AX prompt is NOT
//     triggered by ListWindows/ListMonitors or at adapter
//     construction; it only fires the first time a user invokes Move
//     or Focus.
//  2. Re-fetches the CG window entry by ID (no adapter-level cache
//     of any prior ListWindows result) to get the owner PID and
//     current bounds.
//  3. Walks AXUIElementCreateApplication(pid) → kAXWindowsAttribute
//     and disambiguates the target by matching kAXTitleAttribute and
//     position+size against the CG bounds (small pixel tolerance to
//     paper over the well-known CG-vs-AX shadow-geometry delta).
//  4. For Move: AXUIElementSetAttributeValue for kAXPosition then
//     kAXSize. For Focus: AXUIElementPerformAction kAXRaiseAction
//     followed by [NSRunningApplication activateWithOptions:] on the
//     owning process so the app itself comes forward.
//
// All CoreFoundation / Accessibility memory ownership lives in C
// helpers; Go only sees POD return codes and POD result structs.
package darwin

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices -framework AppKit

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <ApplicationServices/ApplicationServices.h>
#include <objc/runtime.h>
#include <objc/message.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// Return codes shared by wctl_ax_set_bounds and wctl_ax_focus.
//   >= 0  success
//   -1    AX permission denied
//   -2    CG window with the given ID no longer exists
//   -3    AX walk found no matching window for the resolved PID
//   -4    AXUIElementSetAttributeValue / PerformAction failed
//   -5    internal allocation failure
#define WCTL_AX_OK            0
#define WCTL_AX_ERR_DENIED    -1
#define WCTL_AX_ERR_NOTFOUND  -2
#define WCTL_AX_ERR_NOWINDOW  -3
#define WCTL_AX_ERR_SETFAIL   -4
#define WCTL_AX_ERR_INTERNAL  -5

// Pixel tolerance for matching CG bounds against AX position+size
// when title is empty or duplicated. CG reports the visual frame;
// AX reports the structural frame, which on macOS differs by the
// title-bar height (~28px) plus drop shadow. 60px is a pragmatic
// upper bound that absorbs the skew without admitting unrelated
// windows.
#define WCTL_AX_BOUNDS_TOL    60.0

// Plain-old-data struct that Go can read directly. C owns the whole
// CoreFoundation memory dance; Go never touches a CFTypeRef.
typedef struct {
    long   id;
    long   pid;
    char*  title;       // malloc'd UTF-8; may be NULL.
    char*  app;         // malloc'd UTF-8; may be NULL.
    int    has_bounds;
    double x;
    double y;
    double w;
    double h;
} wctl_window_t;

typedef struct {
    int    id_index;     // assigned position in caller's list
    int    primary;      // 0/1
    double x;
    double y;
    double w;
    double h;
} wctl_monitor_t;

static long wctl_dict_long(CFDictionaryRef d, CFStringRef key) {
    CFTypeRef v = CFDictionaryGetValue(d, key);
    if (v == NULL || CFGetTypeID(v) != CFNumberGetTypeID()) return 0;
    long out = 0;
    CFNumberGetValue((CFNumberRef)v, kCFNumberLongType, &out);
    return out;
}

static char* wctl_dict_string(CFDictionaryRef d, CFStringRef key) {
    CFTypeRef v = CFDictionaryGetValue(d, key);
    if (v == NULL || CFGetTypeID(v) != CFStringGetTypeID()) return NULL;
    CFStringRef s = (CFStringRef)v;
    CFIndex len = CFStringGetLength(s);
    CFIndex max = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
    char* buf = (char*)malloc((size_t)max);
    if (!buf) return NULL;
    if (!CFStringGetCString(s, buf, max, kCFStringEncodingUTF8)) {
        free(buf);
        return NULL;
    }
    return buf;
}

static int wctl_dict_bounds(CFDictionaryRef d, CFStringRef key,
                            double* x, double* y, double* w, double* h) {
    CFTypeRef v = CFDictionaryGetValue(d, key);
    if (v == NULL || CFGetTypeID(v) != CFDictionaryGetTypeID()) return 0;
    CGRect r;
    if (!CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)v, &r)) return 0;
    *x = r.origin.x;
    *y = r.origin.y;
    *w = r.size.width;
    *h = r.size.height;
    return 1;
}

// Collect all on-screen windows. Returns count (>= 0) on success or -1 on
// failure. On success *out is a malloc'd array of `count` entries; free
// with wctl_free_windows(out, count).
static int wctl_collect_windows(wctl_window_t** out) {
    *out = NULL;
    CFArrayRef arr = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID
    );
    if (arr == NULL) return -1;

    CFIndex n = CFArrayGetCount(arr);
    wctl_window_t* ws = NULL;
    if (n > 0) {
        ws = (wctl_window_t*)calloc((size_t)n, sizeof(wctl_window_t));
        if (!ws) {
            CFRelease(arr);
            return -1;
        }
    }

    CFStringRef k_num    = CFSTR("kCGWindowNumber");
    CFStringRef k_pid    = CFSTR("kCGWindowOwnerPID");
    CFStringRef k_name   = CFSTR("kCGWindowName");
    CFStringRef k_owner  = CFSTR("kCGWindowOwnerName");
    CFStringRef k_bounds = CFSTR("kCGWindowBounds");
    CFStringRef k_layer  = CFSTR("kCGWindowLayer");

    int out_count = 0;
    for (CFIndex i = 0; i < n; i++) {
        const void* raw = CFArrayGetValueAtIndex(arr, i);
        if (raw == NULL) continue;
        CFDictionaryRef d = (CFDictionaryRef)raw;

        // Layer 0 = normal application window. Anything higher is
        // chrome (menu bar at 25, dock at 20, status items, Spotlight,
        // Control Center, AltTab, etc.) — skip it so `windows list`
        // shows only real app windows. Mirrors the cut already used
        // by wctl_frontmost_window_bounds.
        if (wctl_dict_long(d, k_layer) != 0) continue;

        ws[out_count].id     = wctl_dict_long(d, k_num);
        ws[out_count].pid    = wctl_dict_long(d, k_pid);
        ws[out_count].title  = wctl_dict_string(d, k_name);
        ws[out_count].app    = wctl_dict_string(d, k_owner);
        ws[out_count].has_bounds = wctl_dict_bounds(
            d, k_bounds,
            &ws[out_count].x, &ws[out_count].y,
            &ws[out_count].w, &ws[out_count].h
        );
        out_count++;
    }

    CFRelease(arr);
    *out = ws;
    return out_count;
}

static void wctl_free_windows(wctl_window_t* ws, int count) {
    if (ws == NULL) return;
    for (int i = 0; i < count; i++) {
        if (ws[i].title) free(ws[i].title);
        if (ws[i].app)   free(ws[i].app);
    }
    free(ws);
}

// Cursor location in the global CG coordinate space. Used by the
// public layer to mark which monitor is currently "active" (under the
// pointer). CGEventCreate(NULL) is a stateless query — no events are
// posted; no Accessibility prompt; no Input Monitoring permission
// required.
static void wctl_cursor_pos(double* x, double* y) {
    CGEventRef e = CGEventCreate(NULL);
    if (!e) { *x = 0; *y = 0; return; }
    CGPoint p = CGEventGetLocation(e);
    *x = p.x; *y = p.y;
    CFRelease(e);
}

// Bounds of the topmost on-screen window with kCGWindowLayer == 0
// (the layer normal app windows live on; menu bar, dock, status
// items, etc. live on higher layers and we want to skip them). CG
// returns kCGWindowListOptionOnScreenOnly entries already in z-order
// top-down, so the first match is "frontmost."
//
// Returns 1 with bounds written on success, 0 if no eligible window
// is up (e.g. the desktop is empty / Mission Control is open). The
// public layer marks Focused = monitor containing this rect's center.
static int wctl_frontmost_window_bounds(double* x, double* y,
                                        double* w, double* h) {
    *x = *y = *w = *h = 0;
    CFArrayRef arr = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID
    );
    if (!arr) return 0;

    CFStringRef k_layer  = CFSTR("kCGWindowLayer");
    CFStringRef k_bounds = CFSTR("kCGWindowBounds");
    int found = 0;
    CFIndex n = CFArrayGetCount(arr);
    for (CFIndex i = 0; i < n; i++) {
        CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(arr, i);
        if (!d) continue;
        // Layer 0 = normal application window. Anything higher is
        // chrome (menu bar at 25, dock at 20, status items, etc.).
        if (wctl_dict_long(d, k_layer) != 0) continue;
        if (!wctl_dict_bounds(d, k_bounds, x, y, w, h)) continue;
        if (*w <= 0 || *h <= 0) continue;
        found = 1;
        break;
    }
    CFRelease(arr);
    return found;
}

// Collect all active monitors. Same memory ownership pattern as
// wctl_collect_windows but no nested allocations, so caller just calls
// free() on the returned pointer.
static int wctl_collect_monitors(wctl_monitor_t** out) {
    *out = NULL;
    uint32_t count = 0;
    if (CGGetActiveDisplayList(0, NULL, &count) != kCGErrorSuccess) return -1;
    if (count == 0) return 0;

    CGDirectDisplayID* ids = (CGDirectDisplayID*)calloc(count, sizeof(CGDirectDisplayID));
    if (!ids) return -1;
    if (CGGetActiveDisplayList(count, ids, &count) != kCGErrorSuccess) {
        free(ids);
        return -1;
    }

    wctl_monitor_t* ms = (wctl_monitor_t*)calloc(count, sizeof(wctl_monitor_t));
    if (!ms) { free(ids); return -1; }

    CGDirectDisplayID main = CGMainDisplayID();
    for (uint32_t i = 0; i < count; i++) {
        CGRect r = CGDisplayBounds(ids[i]);
        ms[i].id_index = (int)i;
        ms[i].primary = (ids[i] == main) ? 1 : 0;
        ms[i].x = r.origin.x;
        ms[i].y = r.origin.y;
        ms[i].w = r.size.width;
        ms[i].h = r.size.height;
    }
    free(ids);
    *out = ms;
    return (int)count;
}

// ----------------------------------------------------------------------
// AX (Accessibility) helpers — Move / Focus
// ----------------------------------------------------------------------

// wctl_ax_check returns 1 if the current process is trusted by AX,
// 0 otherwise. We pass kAXTrustedCheckOptionPrompt = false so the
// system permission dialog only fires when the user actually invokes
// Move/Focus and is willing to grant it via System Settings — we
// never silently surprise users from a list path.
//
// Note: passing false for the prompt option still won't trigger the
// dialog from a CLI; the system shows the prompt only for processes
// it can present a UI for. Either way, we just inspect the boolean.
static int wctl_ax_check(void) {
    const void* keys[]   = { kAXTrustedCheckOptionPrompt };
    const void* values[] = { kCFBooleanFalse };
    CFDictionaryRef opts = CFDictionaryCreate(
        kCFAllocatorDefault, keys, values, 1,
        &kCFCopyStringDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );
    Boolean trusted = AXIsProcessTrustedWithOptions(opts);
    if (opts) CFRelease(opts);
    return trusted ? 1 : 0;
}

// wctl_ax_request is the prompt=true sibling of wctl_ax_check, used
// only by the user-invoked `windowctl permissions` subcommand. macOS
// shows its system "wants to control your computer" dialog the first
// time this is called from a given parent process (TCC is keyed per
// parent process). The returned int is the post-call trust state —
// 1 trusted, 0 still denied. The user typically has to grant access
// in System Settings and re-run windowctl, so 0 is a routine outcome
// rather than an error.
static int wctl_ax_request(void) {
    const void* keys[]   = { kAXTrustedCheckOptionPrompt };
    const void* values[] = { kCFBooleanTrue };
    CFDictionaryRef opts = CFDictionaryCreate(
        kCFAllocatorDefault, keys, values, 1,
        &kCFCopyStringDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );
    Boolean trusted = AXIsProcessTrustedWithOptions(opts);
    if (opts) CFRelease(opts);
    return trusted ? 1 : 0;
}

// wctl_window_for_id scans CGWindowListCopyWindowInfo for the entry
// matching `id` and writes the owner PID and bounds + a copy of the
// window title into the out-parameters. Returns 1 on success, 0 if
// the window is gone, -1 on internal failure.
//
// `out_title` is malloc'd UTF-8; caller frees with free(). May be
// NULL on success if the CG entry has no title (untitled documents,
// system overlays, etc.) — the AX matcher handles that case.
static int wctl_window_for_id(long id, long* out_pid,
                              double* out_x, double* out_y,
                              double* out_w, double* out_h,
                              char** out_title) {
    *out_title = NULL;
    CFArrayRef arr = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID
    );
    if (arr == NULL) return -1;

    CFStringRef k_num    = CFSTR("kCGWindowNumber");
    CFStringRef k_pid    = CFSTR("kCGWindowOwnerPID");
    CFStringRef k_name   = CFSTR("kCGWindowName");
    CFStringRef k_bounds = CFSTR("kCGWindowBounds");

    int found = 0;
    CFIndex n = CFArrayGetCount(arr);
    for (CFIndex i = 0; i < n; i++) {
        const void* raw = CFArrayGetValueAtIndex(arr, i);
        if (raw == NULL) continue;
        CFDictionaryRef d = (CFDictionaryRef)raw;
        if (wctl_dict_long(d, k_num) != id) continue;

        *out_pid = wctl_dict_long(d, k_pid);
        if (!wctl_dict_bounds(d, k_bounds, out_x, out_y, out_w, out_h)) {
            CFRelease(arr);
            return -1;
        }
        *out_title = wctl_dict_string(d, k_name);
        found = 1;
        break;
    }

    CFRelease(arr);
    return found ? 1 : 0;
}

// wctl_ax_resolve walks AXUIElementCreateApplication(pid)'s
// kAXWindowsAttribute looking for the window matching the supplied
// CG hints. Returns the AXUIElementRef on success (caller owns;
// CFRelease when done) or NULL on miss / failure.
//
// Matching precedence (best signal first):
//   1) Title + bounds both agree — strongest evidence; rare collisions.
//   2) Title alone agrees — works for most Cocoa apps; bounds may
//      drift by the title-bar/shadow skew.
//   3) Bounds alone agree (within WCTL_AX_BOUNDS_TOL) — needed for
//      Chrome / Safari / Electron apps where AX titles append the
//      browser name and profile (e.g. CG "MyPage", AX "MyPage - Google
//      Chrome - Profile"). CG is the ground truth for which window
//      the user matched on, so when bounds line up we trust it.
//   4) Single AX window for the PID — if the app has exactly one
//      top-level window, there's no ambiguity even when title and
//      bounds both disagree (e.g. mid-animation, Spaces transition).
//
// All four levels are evaluated in one pass; the highest-priority
// non-null hit wins.
// WCTL_AX_DEBUG=1 in the environment enables per-call dumps of the
// PID's AX window list to stderr — kept gated and cheap because the
// CG↔AX title/bounds disagreement that breaks Chrome (and rarely
// other apps) is impossible to debug from outside the walk. Free
// when unset; safe to leave in shipping binaries.
// _AXUIElementGetWindow is private but stable since 10.5; it maps an AX
// window straight to its CGWindowID, so when it answers there is no
// guessing at all.
extern AXError _AXUIElementGetWindow(AXUIElementRef, CGWindowID*);

static AXUIElementRef wctl_ax_resolve_once(pid_t pid, long cg_id, const char* cg_title,
                                       double cx, double cy,
                                       double cw, double ch) {
    int debug = (getenv("WCTL_AX_DEBUG") != NULL);
    AXUIElementRef app = AXUIElementCreateApplication(pid);
    if (!app) {
        if (debug) fprintf(stderr, "[ax] pid=%d AXUIElementCreateApplication=NULL\n", pid);
        return NULL;
    }

    CFArrayRef windows = NULL;
    AXError err = AXUIElementCopyAttributeValue(
        app, kAXWindowsAttribute, (CFTypeRef*)&windows
    );
    if (err != kAXErrorSuccess || windows == NULL) {
        if (debug) fprintf(stderr, "[ax] pid=%d kAXWindowsAttribute err=%d windows=%p\n",
                           pid, err, (void*)windows);
        if (windows) CFRelease(windows);
        CFRelease(app);
        return NULL;
    }
    if (debug) fprintf(stderr, "[ax] pid=%d cg_title=%s cg_bounds=(%.0f,%.0f %.0fx%.0f) ax_window_count=%ld\n",
                       pid, cg_title ? cg_title : "(null)", cx, cy, cw, ch,
                       (long)CFArrayGetCount(windows));

    CFStringRef cg_title_cf = NULL;
    if (cg_title != NULL && cg_title[0] != '\0') {
        cg_title_cf = CFStringCreateWithCString(
            kCFAllocatorDefault, cg_title, kCFStringEncodingUTF8
        );
    }

    AXUIElementRef title_hit       = NULL; // first title match
    AXUIElementRef title_geom_hit  = NULL; // title + bounds match
    AXUIElementRef geom_hit        = NULL; // bounds match (no-title fallback)
    AXUIElementRef id_hit          = NULL; // exact CGWindowID match

    CFIndex n = CFArrayGetCount(windows);
    for (CFIndex i = 0; i < n; i++) {
        AXUIElementRef w = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
        if (!w) continue;

        CGWindowID wid = 0;
        if (id_hit == NULL && cg_id > 0 &&
            _AXUIElementGetWindow(w, &wid) == kAXErrorSuccess && (long)wid == cg_id) {
            id_hit = w;
        }

        // Title check.
        int title_match = 0;
        if (cg_title_cf != NULL) {
            CFStringRef ax_title = NULL;
            AXUIElementCopyAttributeValue(w, kAXTitleAttribute, (CFTypeRef*)&ax_title);
            if (ax_title != NULL) {
                title_match = (CFStringCompare(ax_title, cg_title_cf, 0) == kCFCompareEqualTo);
                CFRelease(ax_title);
            }
        }

        // Bounds check (best-effort; some windows refuse AX position queries).
        int geom_match = 0;
        AXValueRef pos = NULL;
        AXValueRef siz = NULL;
        AXUIElementCopyAttributeValue(w, kAXPositionAttribute, (CFTypeRef*)&pos);
        AXUIElementCopyAttributeValue(w, kAXSizeAttribute,    (CFTypeRef*)&siz);
        CGPoint p = {0, 0};
        CGSize  s = {0, 0};
        if (pos && siz &&
            AXValueGetValue(pos, kAXValueCGPointType, &p) &&
            AXValueGetValue(siz, kAXValueCGSizeType,  &s)) {
            double dx = p.x - cx; if (dx < 0) dx = -dx;
            double dy = p.y - cy; if (dy < 0) dy = -dy;
            double dw = s.width  - cw; if (dw < 0) dw = -dw;
            double dh = s.height - ch; if (dh < 0) dh = -dh;
            geom_match = (dx <= WCTL_AX_BOUNDS_TOL && dy <= WCTL_AX_BOUNDS_TOL &&
                          dw <= WCTL_AX_BOUNDS_TOL && dh <= WCTL_AX_BOUNDS_TOL);
        }
        if (debug) {
            CFStringRef ax_title = NULL;
            AXUIElementCopyAttributeValue(w, kAXTitleAttribute, (CFTypeRef*)&ax_title);
            char tbuf[256] = "(none)";
            if (ax_title) {
                CFStringGetCString(ax_title, tbuf, sizeof(tbuf), kCFStringEncodingUTF8);
                CFRelease(ax_title);
            }
            fprintf(stderr, "[ax]   window[%ld] title=\"%s\" ax_pos=(%.0f,%.0f) ax_size=(%.0fx%.0f) title_match=%d geom_match=%d\n",
                    (long)i, tbuf, p.x, p.y, s.width, s.height, title_match, geom_match);
        }

        if (pos) CFRelease(pos);
        if (siz) CFRelease(siz);

        if (title_match && geom_match && title_geom_hit == NULL) {
            title_geom_hit = w;
        }
        if (title_match && title_hit == NULL) {
            title_hit = w;
        }
        if (geom_match && geom_hit == NULL) {
            geom_hit = w;
        }
    }

    // Single-window fallback: Chrome / Safari / Electron apps
    // sometimes refuse AX position queries during the first ~100ms of
    // a window's life or while a Space animation is in flight, so
    // both title and bounds checks above can come up empty even
    // though the AX tree clearly contains exactly one window for the
    // PID. In that degenerate case the binding is unambiguous.
    AXUIElementRef only_hit = (n == 1) ? (AXUIElementRef)CFArrayGetValueAtIndex(windows, 0) : NULL;

    AXUIElementRef chosen = id_hit;
    if (chosen == NULL) chosen = title_geom_hit;
    if (chosen == NULL) chosen = title_hit;
    if (chosen == NULL) chosen = geom_hit;
    if (chosen == NULL) chosen = only_hit;
    if (chosen != NULL) CFRetain(chosen);
    if (debug) fprintf(stderr, "[ax] chosen=%s\n",
                       chosen == NULL ? "NONE" :
                       chosen == id_hit ? "window-id" :
                       chosen == title_geom_hit ? "title+geom" :
                       chosen == title_hit ? "title" :
                       chosen == geom_hit ? "geom" :
                       chosen == only_hit ? "only-window" : "NONE");

    if (cg_title_cf) CFRelease(cg_title_cf);
    CFRelease(windows);
    CFRelease(app);
    return chosen;
}

// wctl_ax_resolve is wctl_ax_resolve_once plus one retry for Chromium
// and Electron apps, which publish only 0x0 placeholder windows until an
// assistive client sets AXManualAccessibility on the app. Without this a
// Chrome window listed fine but `focus` / `key press --title=` failed
// with "gone from the AX tree".
static AXUIElementRef wctl_ax_resolve(pid_t pid, long cg_id, const char* cg_title,
                                       double cx, double cy,
                                       double cw, double ch) {
    AXUIElementRef w = wctl_ax_resolve_once(pid, cg_id, cg_title, cx, cy, cw, ch);
    if (w) return w;
    AXUIElementRef app = AXUIElementCreateApplication(pid);
    if (!app) return NULL;
    // Chrome often reports an error here yet still turns its tree on,
    // so the result is ignored and the retry decides.
    AXUIElementSetAttributeValue(app, CFSTR("AXManualAccessibility"), kCFBooleanTrue);
    AXUIElementSetAttributeValue(app, CFSTR("AXEnhancedUserInterface"), kCFBooleanTrue);
    CFRelease(app);
    for (int i = 0; i < 10 && !w; i++) {
        usleep(100 * 1000);
        w = wctl_ax_resolve_once(pid, cg_id, cg_title, cx, cy, cw, ch);
    }
    return w;
}

// wctl_ax_set_bounds resolves the AX window for the given CG id and
// applies position+size. See WCTL_AX_ERR_* for return codes.
static int wctl_ax_set_bounds(long id, double nx, double ny, double nw, double nh) {
    if (!wctl_ax_check()) return WCTL_AX_ERR_DENIED;

    long pid = 0;
    double cx = 0, cy = 0, cw = 0, ch = 0;
    char* title = NULL;
    int got = wctl_window_for_id(id, &pid, &cx, &cy, &cw, &ch, &title);
    if (got < 0) { if (title) free(title); return WCTL_AX_ERR_INTERNAL; }
    if (got == 0) { if (title) free(title); return WCTL_AX_ERR_NOTFOUND; }

    AXUIElementRef w = wctl_ax_resolve((pid_t)pid, id, title, cx, cy, cw, ch);
    if (title) free(title);
    if (!w) return WCTL_AX_ERR_NOWINDOW;

    CGPoint p = { nx, ny };
    CGSize  s = { nw, nh };
    AXValueRef pos = AXValueCreate(kAXValueCGPointType, &p);
    AXValueRef siz = AXValueCreate(kAXValueCGSizeType,  &s);
    int rc = WCTL_AX_OK;
    if (!pos || !siz) {
        rc = WCTL_AX_ERR_INTERNAL;
    } else {
        // Set order workaround for macOS AX cross-display moves:
        // when the move spans displays AND resizes, a single
        // pos→size pass leaves position unchanged because
        // kAXSizeAttribute internally re-clamps the window to its
        // current display. The robust sequence is two full passes
        // of pos→size→pos with short yields between writes to let
        // WindowServer commit the intermediate state. This recipe
        // is what Rectangle (PrivateAPI.swift moveWindow) and
        // yabai's window_manager_set_window_frame use.
        AXError last_pos_err  = kAXErrorSuccess;
        AXError last_size_err = kAXErrorSuccess;
        for (int pass = 0; pass < 2; pass++) {
            AXError ep1 = AXUIElementSetAttributeValue(w, kAXPositionAttribute, pos);
            usleep(10000);
            AXError es1 = AXUIElementSetAttributeValue(w, kAXSizeAttribute,     siz);
            usleep(10000);
            AXError ep2 = AXUIElementSetAttributeValue(w, kAXPositionAttribute, pos);
            if (ep1 != kAXErrorSuccess) last_pos_err  = ep1;
            if (es1 != kAXErrorSuccess) last_size_err = es1;
            if (ep2 != kAXErrorSuccess) last_pos_err  = ep2;
        }
        // A refused size (fixed-size windows like Calculator) is not a
        // failure: the caller re-reads the bounds and reports the clamp.
        (void)last_size_err;
        if (last_pos_err != kAXErrorSuccess) {
            rc = WCTL_AX_ERR_SETFAIL;
        }
    }
    if (pos) CFRelease(pos);
    if (siz) CFRelease(siz);
    CFRelease(w);
    return rc;
}

// wctl_ax_focus raises the resolved AX window and activates the
// owning NSRunningApplication so the app itself comes forward (not
// just the window within an already-foreground app).
//
// We reach NSRunningApplication via the Objective-C runtime
// (objc_msgSend) to keep this file pure C — no .m file, no Swift.
// NSApplicationActivateIgnoringOtherApps == 2.
static int wctl_ax_focus(long id) {
    if (!wctl_ax_check()) return WCTL_AX_ERR_DENIED;

    long pid = 0;
    double cx = 0, cy = 0, cw = 0, ch = 0;
    char* title = NULL;
    int got = wctl_window_for_id(id, &pid, &cx, &cy, &cw, &ch, &title);
    if (got < 0) { if (title) free(title); return WCTL_AX_ERR_INTERNAL; }
    if (got == 0) { if (title) free(title); return WCTL_AX_ERR_NOTFOUND; }

    AXUIElementRef w = wctl_ax_resolve((pid_t)pid, id, title, cx, cy, cw, ch);
    if (title) free(title);
    if (!w) return WCTL_AX_ERR_NOWINDOW;

    // Some apps (Calculator) refuse AXRaise yet come forward fine once
    // activated, so a raise error is only fatal if activation can't run.
    AXError raise_err = AXUIElementPerformAction(w, kAXRaiseAction);
    CFRelease(w);
    int activated = 0;

    // [NSRunningApplication runningApplicationWithProcessIdentifier:pid]
    // — reached via the Objective-C runtime so this file stays pure C.
    Class cls = objc_getClass("NSRunningApplication");
    if (cls != NULL) {
        SEL selFor = sel_registerName("runningApplicationWithProcessIdentifier:");
        SEL selAct = sel_registerName("activateWithOptions:");
        // Cast objc_msgSend to typed function pointers so we don't trip
        // the strict-prototype warnings on recent clang/Xcode.
        typedef struct objc_object* (*msg_obj_class_pid_t)(Class, SEL, pid_t);
        typedef signed char (*msg_bool_obj_opts_t)(struct objc_object*, SEL, unsigned long);
        struct objc_object* app =
            ((msg_obj_class_pid_t)objc_msgSend)(cls, selFor, (pid_t)pid);
        if (app != NULL) {
            // NSApplicationActivateIgnoringOtherApps = 1 << 1 = 2
            activated = ((msg_bool_obj_opts_t)objc_msgSend)(app, selAct, 2UL) != 0;
        }
    }
    if (raise_err != kAXErrorSuccess && !activated) return WCTL_AX_ERR_SETFAIL;
    return WCTL_AX_OK;
}

// ---- find: accessibility text walk ----------------------------------
//
// wctl_ax_texts walks the matched window's AX subtree (and the app's
// menu bar, so menus are findable) and returns every element with a
// non-empty label and an on-screen frame. Label precedence: title,
// description, string value, placeholder. Bounded (depth 64, 8000
// elements, 1s AX messaging timeout) so a pathological app cannot hang
// the caller. Strings are malloc'd; free with wctl_free_texts.
typedef struct {
    char* text;
    char* role;
    double x, y, w, h;
} wctl_text_t;

static char* wctl_cf_dup(CFTypeRef v) {
    if (!v || CFGetTypeID(v) != CFStringGetTypeID()) return NULL;
    CFStringRef s = (CFStringRef)v;
    if (CFStringGetLength(s) == 0) return NULL;
    CFIndex max = CFStringGetMaximumSizeForEncoding(CFStringGetLength(s), kCFStringEncodingUTF8) + 1;
    char* buf = (char*)malloc(max);
    if (!buf) return NULL;
    if (!CFStringGetCString(s, buf, max, kCFStringEncodingUTF8)) { free(buf); return NULL; }
    return buf;
}

static char* wctl_ax_str(AXUIElementRef el, CFStringRef attr) {
    CFTypeRef v = NULL;
    if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || !v) return NULL;
    char* out = wctl_cf_dup(v);
    CFRelease(v);
    return out;
}

static int wctl_ax_frame(AXUIElementRef el, double* x, double* y, double* w, double* h) {
    CFTypeRef pv = NULL, sv = NULL;
    CGPoint p; CGSize sz;
    int ok = 0;
    if (AXUIElementCopyAttributeValue(el, kAXPositionAttribute, &pv) == kAXErrorSuccess && pv &&
        AXUIElementCopyAttributeValue(el, kAXSizeAttribute, &sv) == kAXErrorSuccess && sv &&
        AXValueGetValue((AXValueRef)pv, kAXValueCGPointType, &p) &&
        AXValueGetValue((AXValueRef)sv, kAXValueCGSizeType, &sz)) {
        *x = p.x; *y = p.y; *w = sz.width; *h = sz.height;
        ok = sz.width > 0 && sz.height > 0;
    }
    if (pv) CFRelease(pv);
    if (sv) CFRelease(sv);
    return ok;
}

typedef struct { wctl_text_t* items; int n, cap, visited; } wctl_text_acc;

static void wctl_ax_walk(AXUIElementRef el, int depth, wctl_text_acc* acc) {
    if (depth > 64 || acc->visited++ > 8000) return;
    char* label = wctl_ax_str(el, kAXTitleAttribute);
    if (!label) label = wctl_ax_str(el, kAXDescriptionAttribute);
    if (!label) label = wctl_ax_str(el, kAXValueAttribute);
    if (!label) label = wctl_ax_str(el, CFSTR("AXPlaceholderValue"));
    if (!label) {
        // Untitled buttons (window close/minimize/zoom, icon buttons) still
        // have a role description: "close button", "minimize button".
        char* role = wctl_ax_str(el, kAXRoleAttribute);
        if (role && strstr(role, "Button")) label = wctl_ax_str(el, kAXRoleDescriptionAttribute);
        if (role) free(role);
    }
    if (label) {
        double x, y, w, h;
        if (wctl_ax_frame(el, &x, &y, &w, &h)) {
            if (acc->n == acc->cap) {
                acc->cap = acc->cap ? acc->cap * 2 : 256;
                acc->items = (wctl_text_t*)realloc(acc->items, sizeof(wctl_text_t) * acc->cap);
            }
            wctl_text_t* t = &acc->items[acc->n++];
            t->text = label; label = NULL;
            t->role = wctl_ax_str(el, kAXRoleAttribute);
            t->x = x; t->y = y; t->w = w; t->h = h;
        }
        if (label) free(label);
    }
    CFArrayRef kids = NULL;
    if (AXUIElementCopyAttributeValue(el, kAXChildrenAttribute, (CFTypeRef*)&kids) == kAXErrorSuccess && kids) {
        CFIndex n = CFArrayGetCount(kids);
        for (CFIndex i = 0; i < n; i++) {
            wctl_ax_walk((AXUIElementRef)CFArrayGetValueAtIndex(kids, i), depth + 1, acc);
        }
        CFRelease(kids);
    }
}

static int wctl_ax_texts(long id, wctl_text_t** out) {
    *out = NULL;
    if (!wctl_ax_check()) return WCTL_AX_ERR_DENIED;
    long pid = 0;
    double cx = 0, cy = 0, cw = 0, ch = 0;
    char* title = NULL;
    int got = wctl_window_for_id(id, &pid, &cx, &cy, &cw, &ch, &title);
    if (got < 0) { if (title) free(title); return WCTL_AX_ERR_INTERNAL; }
    if (got == 0) { if (title) free(title); return WCTL_AX_ERR_NOTFOUND; }
    AXUIElementRef w = wctl_ax_resolve((pid_t)pid, id, title, cx, cy, cw, ch);
    if (title) free(title);
    if (!w) return WCTL_AX_ERR_NOWINDOW;
    AXUIElementSetMessagingTimeout(w, 1.0);
    wctl_text_acc acc = {0};
    wctl_ax_walk(w, 0, &acc);
    CFRelease(w);
    AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
    if (app) {
        AXUIElementSetMessagingTimeout(app, 1.0);
        CFTypeRef bar = NULL;
        if (AXUIElementCopyAttributeValue(app, kAXMenuBarAttribute, &bar) == kAXErrorSuccess && bar) {
            wctl_ax_walk((AXUIElementRef)bar, 0, &acc);
            CFRelease(bar);
        }
        CFRelease(app);
    }
    *out = acc.items;
    return acc.n;
}

static void wctl_free_texts(wctl_text_t* t, int n) {
    for (int i = 0; i < n; i++) { free(t[i].text); free(t[i].role); }
    free(t);
}
*/
import "C"

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) ListWindows() ([]core.Window, error) {
	var raw *C.wctl_window_t
	n := int(C.wctl_collect_windows(&raw))
	if n < 0 {
		return nil, fmt.Errorf("CGWindowListCopyWindowInfo failed")
	}
	if n == 0 {
		return nil, nil
	}
	defer C.wctl_free_windows(raw, C.int(n))

	ws := unsafe.Slice(raw, n)
	out := make([]core.Window, 0, n)
	for i := 0; i < n; i++ {
		w := core.Window{
			ID:    fmt.Sprintf("%d", int64(ws[i].id)),
			PID:   int(ws[i].pid),
			Title: cStringOrEmpty(ws[i].title),
			App:   cStringOrEmpty(ws[i].app),
		}
		if ws[i].has_bounds != 0 {
			w.Bounds = core.Rect{
				X: int(ws[i].x),
				Y: int(ws[i].y),
				W: int(ws[i].w),
				H: int(ws[i].h),
			}
		}
		out = append(out, w)
	}
	return out, nil
}

func (a *Adapter) ListMonitors() ([]core.Monitor, error) {
	var raw *C.wctl_monitor_t
	n := int(C.wctl_collect_monitors(&raw))
	if n < 0 {
		return nil, fmt.Errorf("CGGetActiveDisplayList failed")
	}
	if n == 0 {
		return nil, nil
	}
	defer C.free(unsafe.Pointer(raw))

	ms := unsafe.Slice(raw, n)
	out := make([]core.Monitor, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, core.Monitor{
			ID:      int(ms[i].id_index),
			X:       int(ms[i].x),
			Y:       int(ms[i].y),
			Width:   int(ms[i].w),
			Height:  int(ms[i].h),
			Primary: ms[i].primary != 0,
		})
	}

	// Stamp Active (cursor) and Focused (frontmost window centroid).
	// Both probes are stateless CG queries, so it's fine to do them
	// every ListMonitors call — list is not on a hot path.
	var cx, cy C.double
	C.wctl_cursor_pos(&cx, &cy)
	markActive(out, int(cx), int(cy))

	var fx, fy, fw, fh C.double
	if C.wctl_frontmost_window_bounds(&fx, &fy, &fw, &fh) != 0 {
		markFocused(out, int(fx)+int(fw)/2, int(fy)+int(fh)/2)
	}
	return out, nil
}

// markActive sets Active=true on whichever monitor contains (px, py).
// No-op if the cursor sits in negative space outside every display
// (rare but possible during a Spaces transition or hot-unplug).
func markActive(ms []core.Monitor, px, py int) {
	for i := range ms {
		m := ms[i]
		if px >= m.X && px < m.X+m.Width && py >= m.Y && py < m.Y+m.Height {
			ms[i].Active = true
			return
		}
	}
}

// markFocused sets Focused=true on whichever monitor contains the
// centroid of the frontmost window. Centroid (not top-left) is the
// right anchor: a window straddling two displays "belongs to" the
// one it occupies more of, matching how macOS itself routes
// keyboard focus.
func markFocused(ms []core.Monitor, cx, cy int) {
	for i := range ms {
		m := ms[i]
		if cx >= m.X && cx < m.X+m.Width && cy >= m.Y && cy < m.Y+m.Height {
			ms[i].Focused = true
			return
		}
	}
}

// moveClampToleranceDarwin is the per-axis pixel slack we tolerate
// between requested and actual post-move bounds before reporting an
// OS-imposed clamp. macOS adds small structural drift via title-bar /
// shadow accounting that's unrelated to a real minimum-size clamp;
// 10px absorbs that without hiding the bigger silent clamps that
// motivate this check (e.g. Chrome refusing < ~833px wide on a
// 1024px-wide display, or AX nudging Y under the menu bar).
const moveClampToleranceDarwin = 10

func (a *Adapter) Move(id string, b core.Rect) error {
	cgID, err := parseCGWindowID(id)
	if err != nil {
		return err
	}
	rc := int(C.wctl_ax_set_bounds(
		C.long(cgID),
		C.double(b.X), C.double(b.Y),
		C.double(b.W), C.double(b.H),
	))
	if err := translateAXReturn(rc, id, "move"); err != nil {
		return err
	}
	// Post-move sanity check: re-read the window's CG bounds and compare
	// against what we asked for. AX reports success even when the OS
	// (or the app's minimum-size constraint) silently clamps the
	// geometry — caller would otherwise see a "successful" move that
	// landed somewhere else. We poll until bounds settle (the
	// WindowServer commits AX size writes immediately but animates the
	// position into a valid frame, so an immediate re-read returns a
	// transient state where W/H is right but X/Y is still mid-animation
	// — that's BUG-13).
	actual, ok := settledWindowBounds(a, id, b)
	if !ok {
		// Window vanished between move and re-read — rare, but not
		// worth failing over; the move itself reported success.
		return nil
	}
	if clampDelta(actual, b) > moveClampToleranceDarwin {
		return fmt.Errorf("requested %dx%d at (%d,%d), OS clamped to %dx%d at (%d,%d) (likely a minimum-window-size constraint)",
			b.W, b.H, b.X, b.Y,
			actual.W, actual.H, actual.X, actual.Y)
	}
	return nil
}

// settledWindowBounds returns the window's bounds once they've stopped
// changing, or once the deadline expires. Fast path (no extra delay):
// if the first read already matches `requested` within tolerance, the
// move landed cleanly and we return immediately. Slow path (clamped
// or animating): poll every 40ms until two consecutive reads agree, or
// 300ms total — whichever comes first. The slow-path cost is only
// paid when the move actually disagrees with the request, so well-
// behaved moves see no latency hit.
func settledWindowBounds(a *Adapter, id string, requested core.Rect) (core.Rect, bool) {
	actual, ok := findWindowBounds(a, id)
	if !ok {
		return core.Rect{}, false
	}
	if clampDelta(actual, requested) <= moveClampToleranceDarwin {
		return actual, true
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(40 * time.Millisecond)
		next, ok := findWindowBounds(a, id)
		if !ok {
			return actual, true
		}
		if next == actual {
			return actual, true
		}
		actual = next
	}
	return actual, true
}

// findWindowBounds re-reads the bounds for a CG window id by listing
// all windows and matching by id. Returns (bounds, true) on hit,
// (zero, false) if the window is no longer enumerable.
func findWindowBounds(a *Adapter, id string) (core.Rect, bool) {
	ws, err := a.ListWindows()
	if err != nil {
		return core.Rect{}, false
	}
	for _, w := range ws {
		if w.ID == id {
			return w.Bounds, true
		}
	}
	return core.Rect{}, false
}

// clampDelta returns the largest per-axis absolute difference between
// the actual and requested rects. Used to decide whether a post-move
// re-read indicates the OS clamped our request.
func clampDelta(actual, requested core.Rect) int {
	d := abs(actual.X - requested.X)
	if v := abs(actual.Y - requested.Y); v > d {
		d = v
	}
	if v := abs(actual.W - requested.W); v > d {
		d = v
	}
	if v := abs(actual.H - requested.H); v > d {
		d = v
	}
	return d
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (a *Adapter) Focus(id string) error {
	cgID, err := parseCGWindowID(id)
	if err != nil {
		return err
	}
	rc := int(C.wctl_ax_focus(C.long(cgID)))
	if rc == -3 && a.focusScriptable(id) {
		return nil
	}
	return translateAXReturn(rc, id, "focus")
}

// scriptableBrowsers publish 0x0 placeholder AX windows (Chrome and its
// relatives) yet answer AppleScript, which can raise a window by title.
var scriptableBrowsers = map[string]bool{
	"Google Chrome": true, "Google Chrome Canary": true, "Chromium": true,
	"Brave Browser": true, "Microsoft Edge": true, "Arc": true, "Vivaldi": true,
}

// focusScriptable is the fallback when AX cannot see the window: it asks
// the browser itself to bring the window with that exact title to the
// front. The caller still verifies focus before sending any input.
func (a *Adapter) focusScriptable(id string) bool {
	ws, err := a.ListWindows()
	if err != nil {
		return false
	}
	for _, w := range ws {
		if w.ID != id || !scriptableBrowsers[w.App] || w.Title == "" {
			continue
		}
		script := `on run argv
tell application (item 1 of argv)
	repeat with w in windows
		if name of w is (item 2 of argv) then
			set index of w to 1
			activate
			return "ok"
		end if
	end repeat
end tell
return "none"
end run`
		out, err := exec.Command("/usr/bin/osascript", "-e", script, w.App, w.Title).Output()
		if err != nil || strings.TrimSpace(string(out)) != "ok" {
			return false
		}
		// AppleScript's activate is ignored when the caller is a
		// background process; LaunchServices is not.
		return exec.Command("/usr/bin/open", "-a", w.App).Run() == nil
	}
	return false
}

// RequestAccessibility triggers the macOS AX trust check WITH prompt
// enabled. This is the only path in windowctl that asks the system to
// surface its "wants to control your computer" dialog. Returns
// core.ErrAccessibilityDenied when the post-call trust state is still
// false, nil when granted. See docs/specs/11-permissions-subcommand.md
// and the wctl_ax_request C helper for context on why this is gated
// behind an explicit user-invoked subcommand rather than fired at
// startup or from list paths.
func (a *Adapter) RequestAccessibility() error {
	if int(C.wctl_ax_request()) == 1 {
		return nil
	}
	return core.ErrAccessibilityDenied
}

// CheckAccessibility is the read-only sibling of RequestAccessibility:
// it returns the current AX trust state without triggering the macOS
// system dialog. Reuses the existing wctl_ax_check C helper, which
// already passes kAXTrustedCheckOptionPrompt = false for the same
// reason — that helper has been the internal pre-flight for
// wctl_ax_set_bounds / wctl_ax_focus since slice 01. Exposing it via a
// Go method (rather than adding a new wctl_ax_check_silent sibling) is
// a deliberate choice: a second helper would be a verbatim duplicate of
// wctl_ax_check, and the existing name + comment block already document
// the prompt=false semantic. See docs/specs/11-permissions-subcommand.md
// ADDED Requirement for the contract.
func (a *Adapter) CheckAccessibility() bool {
	return int(C.wctl_ax_check()) == 1
}

// parseCGWindowID matches the public Window.ID contract — a decimal
// CGWindowID string. Returning a typed error here means callers see
// the same shape Windows/Linux do for malformed IDs.
func parseCGWindowID(id string) (int64, error) {
	var n int64
	if _, err := fmt.Sscanf(id, "%d", &n); err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid macOS window id %q", id)
	}
	return n, nil
}

// translateAXReturn converts the WCTL_AX_ERR_* return codes from the
// C helpers into typed Go errors. See the macro block in the C side
// for the code → meaning mapping.
func translateAXReturn(rc int, id, op string) error {
	switch {
	case rc >= 0:
		return nil
	case rc == -1:
		return core.ErrAccessibilityDenied
	case rc == -2:
		return fmt.Errorf("%w: window %s no longer exists (it may have been closed between list and %s)", core.ErrNoMatch, id, op)
	case rc == -3:
		return fmt.Errorf("%w: window %s is gone from the AX tree (its app may have quit before %s)", core.ErrNoMatch, id, op)
	case rc == -4:
		return fmt.Errorf("windowctl darwin: AX %s of window %s failed", op, id)
	default:
		return fmt.Errorf("windowctl darwin: AX %s of window %s failed (rc=%d)", op, id, rc)
	}
}

func cStringOrEmpty(p *C.char) string {
	if p == nil {
		return ""
	}
	return C.GoString(p)
}

// AccessibleTexts walks the window's AX tree plus its app's menu bar.
func (a *Adapter) AccessibleTexts(id string) ([]core.TextItem, error) {
	wid, err := parseCGWindowID(id)
	if err != nil {
		return nil, err
	}
	var items *C.wctl_text_t
	n := int(C.wctl_ax_texts(C.long(wid), &items))
	if n < 0 {
		return nil, translateAXReturn(n, id, "find")
	}
	if items == nil {
		return nil, nil
	}
	defer C.wctl_free_texts(items, C.int(n))
	out := make([]core.TextItem, 0, n)
	for _, t := range unsafe.Slice(items, n) {
		out = append(out, core.TextItem{
			Text:   C.GoString(t.text),
			Role:   normalizeAXRole(cStringOrEmpty(t.role)),
			Bounds: core.Rect{X: int(t.x), Y: int(t.y), W: int(t.w + 0.5), H: int(t.h + 0.5)},
			Source: "accessibility",
		})
	}
	return out, nil
}

func normalizeAXRole(r string) string {
	switch r {
	case "AXButton", "AXPopUpButton", "AXMenuButton", "AXDisclosureTriangle":
		return "button"
	case "AXMenuItem", "AXMenuBarItem", "AXMenu":
		return "menuitem"
	case "AXLink":
		return "link"
	case "AXTab", "AXRadioButton":
		if r == "AXTab" {
			return "tab"
		}
		return "radio"
	case "AXCheckBox", "AXSwitch", "AXToggle":
		return "checkbox"
	case "AXTextField", "AXTextArea", "AXSearchField", "AXComboBox", "AXSecureTextField":
		return "textfield"
	case "AXStaticText", "AXHeading":
		return "text"
	case "AXImage":
		return "image"
	}
	return "other"
}
