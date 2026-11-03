package groups

import (
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sayNativeExt is what SAPI writes without conversion.
const sayNativeExt = ".wav"

// Speech on Windows is SAPI 5 driven over COM from Go — no PowerShell, no
// helper process. Voices come from two token categories: the OneCore one
// (the modern voices, e.g. "Microsoft David", "Microsoft Mark") and the
// classic desktop one ("Microsoft David Desktop"). SAPI's SpVoice speaks with
// either kind of token, so one engine covers both.
//
// Raw vtable calls, in the style of internal/engine/adapter/windows/find.go.
// Indices count IUnknown's three methods first.

var (
	sapiOle32        = windows.NewLazySystemDLL("ole32.dll")
	sapiCoInit       = sapiOle32.NewProc("CoInitializeEx")
	sapiCoUninit     = sapiOle32.NewProc("CoUninitialize")
	sapiCoCreate     = sapiOle32.NewProc("CoCreateInstance")
	sapiCoTaskFree   = sapiOle32.NewProc("CoTaskMemFree")
	sapiPlaySound    = windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")
	sapiLCIDToLocale = windows.NewLazySystemDLL("kernel32.dll").NewProc("LCIDToLocaleName")
)

var (
	clsidSpVoice               = windows.GUID{Data1: 0x96749377, Data2: 0x3391, Data3: 0x11D2, Data4: [8]byte{0x9E, 0xE3, 0x00, 0xC0, 0x4F, 0x79, 0x73, 0x96}}
	iidISpVoice                = windows.GUID{Data1: 0x6C44DF74, Data2: 0x72B9, Data3: 0x4992, Data4: [8]byte{0xA1, 0xEC, 0xEF, 0x99, 0x6E, 0x04, 0x22, 0xD4}}
	clsidSpStream              = windows.GUID{Data1: 0x715D9C59, Data2: 0x4442, Data3: 0x11D2, Data4: [8]byte{0x96, 0x05, 0x00, 0xC0, 0x4F, 0x8E, 0xE6, 0x28}}
	iidISpStream               = windows.GUID{Data1: 0x12E3CCA9, Data2: 0x7518, Data3: 0x44C5, Data4: [8]byte{0xA5, 0xE7, 0xBA, 0x5A, 0x79, 0xCB, 0x92, 0x9E}}
	clsidSpObjectTokenCategory = windows.GUID{Data1: 0xA910187F, Data2: 0x0C7A, Data3: 0x45AC, Data4: [8]byte{0x92, 0xCC, 0x59, 0xED, 0xAF, 0xB7, 0x7B, 0x53}}
	iidISpObjectTokenCategory  = windows.GUID{Data1: 0x2D3D3845, Data2: 0x39AF, Data3: 0x4850, Data4: [8]byte{0xBB, 0xF9, 0x40, 0xB4, 0x97, 0x80, 0x01, 0x1D}}
	spdfidWaveFormatEx         = windows.GUID{Data1: 0xC31ADBAE, Data2: 0x527F, Data3: 0x4FF5, Data4: [8]byte{0xA2, 0x30, 0xF6, 0x2B, 0xB6, 0x1F, 0xF7, 0x0C}}
)

// vtable indices
const (
	// ISpDataKey (ISpObjectToken and ISpObjectTokenCategory start with it)
	dkGetStringValue = 6
	dkOpenKey        = 9
	// ISpObjectTokenCategory
	catSetID      = 15
	catEnumTokens = 18
	// IEnumSpObjectTokens
	enumItem     = 7
	enumGetCount = 8
	// ISpStream (after IStream 3..13 and ISpStreamFormat 14)
	streamBindToFile = 17
	streamClose      = 18
	// ISpVoice (after ISpNotifySource 3..9 and ISpEventSource 10..12)
	voiceSetOutput = 13
	voiceSetVoice  = 18
	voiceSpeak     = 20
	voiceSetRate   = 28
)

const (
	sapiCategoryOneCore = `HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Speech_OneCore\Voices`
	sapiCategoryDesktop = `HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Speech\Voices`
)

// waveFormatEx is WAVEFORMATEX (18 bytes; Go pads the tail, which is harmless).
type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	CbSize         uint16
}

func sapiPtr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

func sapiCall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(sapiPtr(obj))
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func sapiRelease(obj uintptr) {
	if obj != 0 {
		sapiCall(obj, 2)
	}
}

func sapiFailed(hr uintptr) bool { return int32(hr) < 0 }

func sapiErr(what string, hr uintptr) error {
	return fmt.Errorf("speech: %s failed (HRESULT 0x%08X)", what, uint32(hr))
}

func sapiCreate(clsid, iid *windows.GUID, what string) (uintptr, error) {
	var obj uintptr
	hr, _, _ := sapiCoCreate.Call(uintptr(unsafe.Pointer(clsid)), 0, 1 /* CLSCTX_INPROC_SERVER */, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&obj)))
	if sapiFailed(hr) {
		return 0, sapiErr("CoCreateInstance("+what+")", hr)
	}
	return obj, nil
}

// withCOM runs fn on a locked OS thread with COM initialised.
func withCOM(fn func() error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if hr, _, _ := sapiCoInit.Call(0, 2 /* COINIT_APARTMENTTHREADED */); hr == 0 || hr == 1 {
			defer sapiCoUninit.Call()
		}
		done <- fn()
	}()
	return <-done
}

func utf16p(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

// dataKeyString reads one string value of an ISpDataKey ("" name = default).
func dataKeyString(key uintptr, name string) string {
	var namePtr *uint16
	if name != "" {
		namePtr = utf16p(name)
	}
	var out uintptr
	hr := sapiCall(key, dkGetStringValue, uintptr(unsafe.Pointer(namePtr)), uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(namePtr)
	if sapiFailed(hr) || out == 0 {
		return ""
	}
	defer sapiCoTaskFree.Call(out)
	return windows.UTF16PtrToString((*uint16)(sapiPtr(out)))
}

type sapiToken struct {
	ptr   uintptr
	voice sayVoice
}

// sapiTokens lists the voice tokens of one category. The caller releases them.
func sapiTokens(category, engine string) ([]sapiToken, error) {
	cat, err := sapiCreate(&clsidSpObjectTokenCategory, &iidISpObjectTokenCategory, "SpObjectTokenCategory")
	if err != nil {
		return nil, err
	}
	defer sapiRelease(cat)
	id := utf16p(category)
	if hr := sapiCall(cat, catSetID, uintptr(unsafe.Pointer(id)), 0); sapiFailed(hr) {
		return nil, sapiErr("SetId", hr) // the category is not installed
	}
	runtime.KeepAlive(id)
	var enum uintptr
	if hr := sapiCall(cat, catEnumTokens, 0, 0, uintptr(unsafe.Pointer(&enum))); sapiFailed(hr) {
		return nil, sapiErr("EnumTokens", hr)
	}
	defer sapiRelease(enum)
	var n uint32
	sapiCall(enum, enumGetCount, uintptr(unsafe.Pointer(&n)))
	var out []sapiToken
	for i := uint32(0); i < n; i++ {
		var tok uintptr
		if hr := sapiCall(enum, enumItem, uintptr(i), uintptr(unsafe.Pointer(&tok))); sapiFailed(hr) || tok == 0 {
			continue
		}
		v := sayVoice{Engine: engine}
		var attrs uintptr
		attrName := utf16p("Attributes")
		if hr := sapiCall(tok, dkOpenKey, uintptr(unsafe.Pointer(attrName)), uintptr(unsafe.Pointer(&attrs))); !sapiFailed(hr) && attrs != 0 {
			v.Name = dataKeyString(attrs, "Name")
			v.Gender = dataKeyString(attrs, "Gender")
			v.Language = lcidName(dataKeyString(attrs, "Language"))
			sapiRelease(attrs)
		}
		runtime.KeepAlive(attrName)
		if v.Name == "" {
			v.Name = dataKeyString(tok, "")
		}
		out = append(out, sapiToken{ptr: tok, voice: v})
	}
	return out, nil
}

// lcidName turns SAPI's hex LCID list ("409;9") into a locale name ("en-US").
func lcidName(raw string) string {
	first, _, _ := strings.Cut(raw, ";")
	lcid, err := strconv.ParseUint(strings.TrimSpace(first), 16, 32)
	if err != nil {
		return raw
	}
	buf := make([]uint16, 85)
	if n, _, _ := sapiLCIDToLocale.Call(uintptr(lcid), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); n == 0 {
		return raw
	}
	return windows.UTF16ToString(buf)
}

// allTokens is OneCore first, then desktop SAPI; a category that is not
// installed is skipped.
func allTokens() []sapiToken {
	var out []sapiToken
	for _, c := range []struct{ id, engine string }{{sapiCategoryOneCore, "onecore"}, {sapiCategoryDesktop, "sapi"}} {
		if toks, err := sapiTokens(c.id, c.engine); err == nil {
			out = append(out, toks...)
		}
	}
	return out
}

func releaseTokens(toks []sapiToken) {
	for _, t := range toks {
		sapiRelease(t.ptr)
	}
}

func sayVoices() ([]sayVoice, error) {
	var voices []sayVoice
	err := withCOM(func() error {
		toks := allTokens()
		defer releaseTokens(toks)
		seen := map[string]bool{}
		for _, t := range toks {
			if t.voice.Name == "" || seen[strings.ToLower(t.voice.Name)] {
				continue
			}
			seen[strings.ToLower(t.voice.Name)] = true
			voices = append(voices, t.voice)
		}
		return nil
	})
	return voices, err
}

// voiceCandidates are the tokens matching want: exact names first, then
// partial ones, OneCore before desktop.
func voiceCandidates(toks []sapiToken, want string) []sapiToken {
	w := strings.ToLower(strings.TrimSpace(want))
	var exact, partial []sapiToken
	for _, t := range toks {
		n := strings.ToLower(t.voice.Name)
		switch {
		case n == w || n == "microsoft "+w:
			exact = append(exact, t)
		case strings.Contains(n, w):
			partial = append(partial, t)
		}
	}
	return append(exact, partial...)
}

// sapiRate maps words per minute onto SAPI's -10..10 (0 is about 175 wpm).
func sapiRate(wpm int) int {
	r := int(math.Round(float64(wpm-sayDefaultWPM) / 25))
	return max(-10, min(10, r))
}

func sayRender(req sayRequest, out string) error {
	return withCOM(func() error {
		var candidates []sapiToken
		if req.Voice != "" {
			toks := allTokens()
			defer releaseTokens(toks)
			if candidates = voiceCandidates(toks, req.Voice); len(candidates) == 0 {
				return fmt.Errorf("no voice named %q (list voices with `aos say voices`)", req.Voice)
			}
		}
		// Try each matching token: a OneCore voice SAPI cannot drive falls
		// through to the desktop voice of the same name.
		var last error
		for i := 0; i == 0 || i < len(candidates); i++ {
			var tok uintptr
			if len(candidates) > 0 {
				tok = candidates[i].ptr
			}
			if last = sapiSpeakToFile(req, tok, out); last == nil {
				return nil
			}
		}
		return last
	})
}

// sapiSpeakToFile renders text into a 22.05 kHz 16-bit mono wav.
func sapiSpeakToFile(req sayRequest, token uintptr, out string) error {
	voice, err := sapiCreate(&clsidSpVoice, &iidISpVoice, "SpVoice")
	if err != nil {
		return err
	}
	defer sapiRelease(voice)
	if token != 0 {
		if hr := sapiCall(voice, voiceSetVoice, token); sapiFailed(hr) {
			return sapiErr("SetVoice", hr)
		}
	}
	if req.Rate > 0 {
		sapiCall(voice, voiceSetRate, uintptr(sapiRate(req.Rate)))
	}
	stream, err := sapiCreate(&clsidSpStream, &iidISpStream, "SpStream")
	if err != nil {
		return err
	}
	defer sapiRelease(stream)
	wf := waveFormatEx{FormatTag: 1, Channels: 1, SamplesPerSec: 22050, AvgBytesPerSec: 44100, BlockAlign: 2, BitsPerSample: 16}
	path := utf16p(out)
	hr := sapiCall(stream, streamBindToFile, uintptr(unsafe.Pointer(path)), 3, /* SPFM_CREATE_ALWAYS */
		uintptr(unsafe.Pointer(&spdfidWaveFormatEx)), uintptr(unsafe.Pointer(&wf)), 0)
	runtime.KeepAlive(path)
	runtime.KeepAlive(&wf)
	if sapiFailed(hr) {
		return sapiErr("BindToFile", hr)
	}
	defer sapiCall(stream, streamClose)
	if hr := sapiCall(voice, voiceSetOutput, stream, 1); sapiFailed(hr) {
		return sapiErr("SetOutput", hr)
	}
	text := utf16p(req.Text)
	hr = sapiCall(voice, voiceSpeak, uintptr(unsafe.Pointer(text)), 0x10 /* SPF_IS_NOT_XML, synchronous */, 0)
	runtime.KeepAlive(text)
	if sapiFailed(hr) {
		return sapiErr("Speak", hr)
	}
	return nil
}

// playClip plays a wav in this process and returns when it ends; `say stop`
// ends it by ending this process.
func playClip(_ string, clip string) error {
	const sndFilename, sndNoDefault = 0x00020000, 0x0002
	p := utf16p(clip)
	ok, _, err := sapiPlaySound.Call(uintptr(unsafe.Pointer(p)), 0, sndFilename|sndNoDefault)
	runtime.KeepAlive(p)
	if ok == 0 {
		return fmt.Errorf("PlaySound: %v", err)
	}
	return nil
}

func killTree(pid int) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.TerminateProcess(h, 1)
}
