package groups

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// volumeInfo is one mounted volume, in the shape `disk list --json` emits.
// Free is what the current user can still write (it excludes blocks reserved
// for root), which is the number that answers "how much space is left".
type volumeInfo struct {
	Mount       string  `json:"mount"`
	Device      string  `json:"device,omitempty"`
	FSType      string  `json:"fs_type,omitempty"`
	Label       string  `json:"label,omitempty"`
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedPercent float64 `json:"used_percent"`
	Removable   bool    `json:"removable"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("disk", "Volumes: free and total space, eject removable drives")
		r.Add(
			&cli.Command{
				Group: "disk", Name: "list",
				Summary: "List mounted volumes with total, used and free space",
				Args:    "[--json]",
				Examples: []string{
					"aos disk list",
					"aos disk list --json",
				},
				Run: runDiskList,
			},
			&cli.Command{
				Group: "disk", Name: "usage",
				Summary: "Print total, used and free space of the volume holding a path",
				Args:    "[<path>] [--json]",
				Examples: []string{
					"aos disk usage              # the volume holding your home folder",
					"aos disk usage /Volumes/USB --json",
					"aos disk usage D:\\",
				},
				Run: runDiskUsage,
			},
			&cli.Command{
				Group: "disk", Name: "eject",
				Summary: "Unmount and eject a removable volume, by mount point or device",
				Args:    "<mount|device>",
				Examples: []string{
					"aos disk eject /Volumes/USB",
					"aos disk eject /dev/sdb1",
					"aos disk eject E:",
				},
				Run: runDiskEject,
			},
		)
	})
}

func runDiskList(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("disk list", args)
	if err != nil {
		return err
	}
	vols, err := listVolumes()
	if err != nil {
		return err
	}
	for i := range vols {
		vols[i].finish()
	}
	if asJSON {
		if vols == nil {
			vols = []volumeInfo{}
		}
		return writeJSON(c, vols)
	}
	for _, v := range vols {
		c.Println(volumeLine(v))
	}
	return nil
}

func runDiskUsage(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	if len(set.Rest) > 1 {
		return fmt.Errorf("`disk usage` takes at most one path")
	}
	path := ""
	if len(set.Rest) == 1 {
		path = expandHome(set.Rest[0])
	} else if path, err = os.UserHomeDir(); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("no such path: %s", path)}
	}
	v, err := volumeOf(path)
	if err != nil {
		return err
	}
	v.finish()
	if set.Has("json") {
		return writeJSON(c, v)
	}
	c.Println(volumeLine(v))
	return nil
}

func runDiskEject(c *cli.Ctx, args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "--") {
		return fmt.Errorf("`disk eject` needs one mount point or device, e.g. /Volumes/USB, /dev/sdb1 or E:")
	}
	target := args[0]
	if isSystemVolume(target) {
		return fmt.Errorf("refusing to eject the system volume %s", target)
	}
	if err := ejectVolume(target); err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	c.Printf("ejected %s\n", target)
	return nil
}

func (v *volumeInfo) finish() {
	if v.UsedBytes == 0 && v.TotalBytes >= v.FreeBytes {
		v.UsedBytes = v.TotalBytes - v.FreeBytes
	}
	if v.TotalBytes > 0 {
		v.UsedPercent = float64(int(float64(v.UsedBytes)*1000/float64(v.TotalBytes)+0.5)) / 10
	}
}

func volumeLine(v volumeInfo) string {
	line := fmt.Sprintf("%-24s %9s free of %9s  (%.0f%% used)", v.Mount, humanBytes(v.FreeBytes), humanBytes(v.TotalBytes), v.UsedPercent)
	if v.Label != "" {
		line += "  " + v.Label
	}
	if v.Removable {
		line += "  removable"
	}
	return line
}

// isSystemVolume catches the eject targets that are never what was meant.
func isSystemVolume(target string) bool {
	t := strings.TrimRight(strings.ToUpper(target), `\/`)
	return target == "/" || t == "C:" || t == "/SYSTEM/VOLUMES/DATA"
}

var plistBool = regexp.MustCompile(`<key>(Ejectable|Removable|RemovableMedia)</key>\s*<(true|false)/>`)

// plistRemovable reads `diskutil info -plist`: a volume is removable when
// macOS says it can be ejected or sits on removable media.
func plistRemovable(plist string) bool {
	for _, m := range plistBool.FindAllStringSubmatch(plist, -1) {
		if m[2] == "true" {
			return true
		}
	}
	return false
}

// mountEntry is one line of Linux /proc/mounts.
type mountEntry struct{ Device, Mount, FSType string }

// parseProcMounts keeps the mounts backed by a real block device, decoding
// the octal escapes /proc/mounts uses for spaces in paths.
func parseProcMounts(text string) []mountEntry {
	var out []mountEntry
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[0], "/dev/") || strings.HasPrefix(f[0], "/dev/loop") {
			continue
		}
		mount := unescapeMount(f[1])
		if seen[mount] || strings.HasPrefix(mount, "/snap/") || strings.HasPrefix(mount, "/boot/efi") {
			continue
		}
		seen[mount] = true
		out = append(out, mountEntry{Device: f[0], Mount: mount, FSType: f[2]})
	}
	return out
}

func unescapeMount(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// blockDeviceName maps a partition to its disk for /sys/block/<disk>/removable:
// sdb1 -> sdb, nvme0n1p2 -> nvme0n1, mmcblk0p1 -> mmcblk0.
func blockDeviceName(dev string) string {
	name := strings.TrimPrefix(dev, "/dev/")
	if strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "mmcblk") {
		if i := strings.LastIndex(name, "p"); i > 0 && i < len(name)-1 {
			if _, err := strconv.Atoi(name[i+1:]); err == nil {
				return name[:i]
			}
		}
		return name
	}
	return strings.TrimRight(name, "0123456789")
}
