package groups

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"
)

func TestParseXBEL(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<xbel version="1.0" xmlns:bookmark="http://www.freedesktop.org/standards/desktop-bookmarks">
  <bookmark href="file:///home/me/old%20notes.txt" added="2026-11-01T10:00:00Z" modified="2026-11-01T10:00:00Z" visited="2026-11-01T10:00:00Z">
    <info><metadata owner="http://freedesktop.org">
      <bookmark:applications><bookmark:application name="gedit" exec="gedit %u" modified="2026-11-01T10:00:00Z" count="1"/></bookmark:applications>
    </metadata></info>
  </bookmark>
  <bookmark href="file:///home/me/report.odt" added="2026-11-05T10:00:00Z" modified="2026-11-05T10:00:00Z" visited="2026-11-06T09:00:00.123456Z">
    <info><metadata owner="http://freedesktop.org">
      <bookmark:applications><bookmark:application name="LibreOffice" exec="soffice %u" modified="2026-11-06T09:00:00Z" count="3"/></bookmark:applications>
    </metadata></info>
  </bookmark>
  <bookmark href="https://example.com/x" added="2026-11-07T10:00:00Z" modified="2026-11-07T10:00:00Z" visited="2026-11-07T10:00:00Z"/>
</xbel>`)
	all, err := parseXBEL(doc, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Path != "/home/me/report.odt" || all[1].Path != "/home/me/old notes.txt" {
		t.Errorf("all = %+v", all)
	}
	if all[0].App != "LibreOffice" {
		t.Errorf("app = %q", all[0].App)
	}
	only, _ := parseXBEL(doc, "gedit")
	if len(only) != 1 || only[0].Path != "/home/me/old notes.txt" {
		t.Errorf("--app=gedit = %+v", only)
	}
}

// buildLnk makes a minimal shell link with LinkInfo and a local base path.
func buildLnk(ansi string, unicode string) []byte {
	b := make([]byte, 0x4C)
	binary.LittleEndian.PutUint32(b, 0x4C)
	binary.LittleEndian.PutUint32(b[0x14:], 0x1|0x2) // IDList + LinkInfo
	b = append(b, 2, 0, 0, 0)                        // IDList: size 2, then just the terminal ItemID
	headerSize := uint32(0x1C)
	if unicode != "" {
		headerSize = 0x24
	}
	info := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(info[4:], headerSize)
	binary.LittleEndian.PutUint32(info[8:], 0x1)
	binary.LittleEndian.PutUint32(info[0x10:], uint32(len(info)))
	info = append(info, append([]byte(ansi), 0)...)
	if unicode != "" {
		binary.LittleEndian.PutUint32(info[0x1C:], uint32(len(info)))
		for _, u := range utf16.Encode([]rune(unicode)) {
			info = binary.LittleEndian.AppendUint16(info, u)
		}
		info = append(info, 0, 0)
	}
	binary.LittleEndian.PutUint32(info, uint32(len(info)))
	return append(b, info...)
}

func TestLnkTarget(t *testing.T) {
	if got := lnkTarget(buildLnk(`C:\Users\me\a.txt`, "")); got != `C:\Users\me\a.txt` {
		t.Errorf("ansi = %q", got)
	}
	if got := lnkTarget(buildLnk(`C:\x`, `C:\Users\me\résumé.docx`)); got != `C:\Users\me\résumé.docx` {
		t.Errorf("unicode = %q", got)
	}
	if lnkTarget([]byte("not a link")) != "" {
		t.Error("garbage should give no target")
	}
}

func TestKeepExistingAndOrder(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	os.WriteFile(a, nil, 0o644)
	os.WriteFile(b, nil, 0o644)
	now := time.Now()
	files := []recentFile{
		{Path: a, when: now.Add(-time.Hour)},
		{Path: filepath.Join(dir, "gone.txt"), when: now},
		{Path: b, when: now},
		{Path: a, when: now.Add(-2 * time.Hour)},
	}
	newestFirst(files)
	got := keepExisting(files)
	if len(got) != 2 || got[0].Path != b || got[1].Path != a || got[0].Name != "b.txt" || got[0].Modified == "" {
		t.Errorf("got %+v", got)
	}
}

func TestRecentAppMatches(t *testing.T) {
	if !recentAppMatches("", "anything") || !recentAppMatches("textedit", "com.apple.TextEdit") || recentAppMatches("code", "com.apple.Preview") {
		t.Error("recentAppMatches wrong")
	}
}
