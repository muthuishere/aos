package groups

import (
	"strings"

	"github.com/muthuishere/aos/internal/sys"
	"golang.org/x/sys/unix"
)

// listVolumes lists the volumes Finder shows: local, device-backed, and not
// hidden system volumes (the APFS VM, Preboot, Data and friends).
func listVolumes() ([]volumeInfo, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	buf := make([]unix.Statfs_t, n)
	if n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT); err != nil {
		return nil, err
	}
	var out []volumeInfo
	for _, s := range buf[:n] {
		dev := unix.ByteSliceToString(s.Mntfromname[:])
		if s.Flags&unix.MNT_DONTBROWSE != 0 || !strings.HasPrefix(dev, "/dev/") {
			continue
		}
		v := darwinVolume(s)
		if strings.HasPrefix(v.Mount, "/Volumes/") {
			if plist, err := sys.Output("diskutil", "info", "-plist", v.Mount); err == nil {
				v.Removable = plistRemovable(plist)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func darwinVolume(s unix.Statfs_t) volumeInfo {
	bs := uint64(s.Bsize)
	return volumeInfo{
		Mount:      unix.ByteSliceToString(s.Mntonname[:]),
		Device:     unix.ByteSliceToString(s.Mntfromname[:]),
		FSType:     unix.ByteSliceToString(s.Fstypename[:]),
		TotalBytes: s.Blocks * bs,
		FreeBytes:  s.Bavail * bs,
		UsedBytes:  (s.Blocks - s.Bfree) * bs,
	}
}

func volumeOf(path string) (volumeInfo, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return volumeInfo{}, err
	}
	return darwinVolume(s), nil
}

func ejectVolume(target string) error {
	_, err := sys.Output("diskutil", "eject", target)
	return err
}
