package groups

import (
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/muthuishere/aos/internal/cli"
)

// recentFile is one recently opened document, in `file recent --json` shape.
type recentFile struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	App      string `json:"app,omitempty"`
	Modified string `json:"modified,omitempty"`
	when     time.Time
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "file", Name: "recent",
			Summary: "List recently opened documents, newest first, from the OS's own recents",
			Args:    "[--app=<name>] [--limit=<n>] [--json]",
			Examples: []string{
				"aos file recent",
				"aos file recent --app=textedit --limit=5",
				"aos file recent --json",
			},
			Run: runFileRecent,
		})
	})
}

func runFileRecent(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "limit")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "limit", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`file recent` takes no positional arguments")
	}
	limit, err := set.Int("limit", 20)
	if err != nil || limit < 1 {
		return fmt.Errorf("--limit must be a positive number")
	}
	files, err := listRecent(strings.ToLower(strings.TrimSpace(set.String("app", ""))))
	if err != nil {
		return err
	}
	files = keepExisting(files)
	if len(files) > limit {
		files = files[:limit]
	}
	if set.Has("json") {
		if files == nil {
			files = []recentFile{}
		}
		return writeJSON(c, files)
	}
	for _, f := range files {
		c.Println(f.Path)
	}
	return nil
}

// keepExisting drops entries whose file is gone or listed twice, filling in
// the name and modified time from the file itself.
func keepExisting(in []recentFile) []recentFile {
	seen := map[string]bool{}
	var out []recentFile
	for _, f := range in {
		if f.Path == "" || seen[f.Path] {
			continue
		}
		st, err := os.Stat(f.Path)
		if err != nil {
			continue
		}
		seen[f.Path] = true
		f.Name = filepath.Base(f.Path)
		f.Modified = st.ModTime().Format(time.RFC3339)
		out = append(out, f)
	}
	return out
}

// newestFirst orders entries by their recency stamp, keeping the source
// order among equals.
func newestFirst(files []recentFile) {
	sort.SliceStable(files, func(i, j int) bool { return files[i].when.After(files[j].when) })
}

// recentAppMatches is the --app filter: "" matches everything, otherwise a
// case-insensitive substring of the app name or id.
func recentAppMatches(filter, app string) bool {
	return filter == "" || strings.Contains(strings.ToLower(app), filter)
}

// xbel is the subset of freedesktop recently-used.xbel aos reads.
type xbel struct {
	Bookmarks []struct {
		Href     string `xml:"href,attr"`
		Modified string `xml:"modified,attr"`
		Visited  string `xml:"visited,attr"`
		Apps     []struct {
			Name     string `xml:"name,attr"`
			Modified string `xml:"modified,attr"`
		} `xml:"info>metadata>applications>application"`
	} `xml:"bookmark"`
}

// parseXBEL reads ~/.local/share/recently-used.xbel, newest first.
func parseXBEL(data []byte, app string) ([]recentFile, error) {
	var doc xbel
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var out []recentFile
	for _, b := range doc.Bookmarks {
		u, err := url.Parse(b.Href)
		if err != nil || u.Scheme != "file" {
			continue
		}
		f := recentFile{Path: u.Path, when: latestStamp(b.Visited, b.Modified)}
		matched := app == ""
		for _, a := range b.Apps {
			if recentAppMatches(app, a.Name) {
				matched = true
				if f.App == "" {
					f.App = a.Name
				}
				if app != "" {
					f.when = latestStamp(a.Modified, b.Visited)
				}
			}
		}
		if matched {
			out = append(out, f)
		}
	}
	newestFirst(out)
	return out, nil
}

func latestStamp(stamps ...string) time.Time {
	var best time.Time
	for _, s := range stamps {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil && t.After(best) {
			best = t
		}
	}
	return best
}

// lnkTarget extracts the local target path from a Windows .lnk (MS-SHLLINK):
// skip the header and the ID list, then read LinkInfo's local base path,
// preferring the Unicode copy when the LinkInfo header carries one.
func lnkTarget(b []byte) string {
	const headerSize = 0x4C
	if len(b) < headerSize || binary.LittleEndian.Uint32(b) != headerSize {
		return ""
	}
	flags := binary.LittleEndian.Uint32(b[0x14:])
	off := headerSize
	if flags&0x1 != 0 { // HasLinkTargetIDList
		if off+2 > len(b) {
			return ""
		}
		off += 2 + int(binary.LittleEndian.Uint16(b[off:]))
	}
	if flags&0x2 == 0 || off+0x1C > len(b) { // HasLinkInfo
		return ""
	}
	info := b[off:]
	infoSize := int(binary.LittleEndian.Uint32(info))
	infoHeader := binary.LittleEndian.Uint32(info[4:])
	if infoSize > len(info) || binary.LittleEndian.Uint32(info[8:])&0x1 == 0 { // VolumeIDAndLocalBasePath
		return ""
	}
	info = info[:infoSize]
	if infoHeader >= 0x24 && len(info) >= 0x24 {
		if p := int(binary.LittleEndian.Uint32(info[0x1C:])); p > 0 && p < len(info) {
			return utf16z(info[p:])
		}
	}
	p := int(binary.LittleEndian.Uint32(info[0x10:]))
	if p <= 0 || p >= len(info) {
		return ""
	}
	end := p
	for end < len(info) && info[end] != 0 {
		end++
	}
	return string(info[p:end])
}

func utf16z(b []byte) string {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		ch := binary.LittleEndian.Uint16(b[i:])
		if ch == 0 {
			break
		}
		u = append(u, ch)
	}
	return string(utf16Decode(u))
}

func utf16Decode(u []uint16) []rune { return utf16.Decode(u) }
