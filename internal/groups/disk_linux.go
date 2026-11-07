package groups

import (
	"fmt"
	"os"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
	"golang.org/x/sys/unix"
)

func listVolumes() ([]volumeInfo, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, err
	}
	var out []volumeInfo
	for _, m := range parseProcMounts(string(data)) {
		v := volumeInfo{Mount: m.Mount, Device: m.Device, FSType: m.FSType}
		if statVolume(m.Mount, &v) != nil {
			continue
		}
		if b, err := os.ReadFile("/sys/block/" + blockDeviceName(m.Device) + "/removable"); err == nil {
			v.Removable = strings.TrimSpace(string(b)) == "1"
		}
		out = append(out, v)
	}
	return out, nil
}

// volumeOf finds the mount holding path: the longest mount point that
// prefixes it, so device and removable come along with the sizes.
func volumeOf(path string) (volumeInfo, error) {
	v := volumeInfo{Mount: path}
	if vols, err := listVolumes(); err == nil {
		best := -1
		for i, cand := range vols {
			if strings.HasPrefix(path+"/", strings.TrimSuffix(cand.Mount, "/")+"/") && (best < 0 || len(cand.Mount) > len(vols[best].Mount)) {
				best = i
			}
		}
		if best >= 0 {
			v = vols[best]
		}
	}
	return v, statVolume(path, &v)
}

func statVolume(path string, v *volumeInfo) error {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return err
	}
	bs := uint64(st.Bsize)
	v.TotalBytes = st.Blocks * bs
	v.FreeBytes = st.Bavail * bs
	v.UsedBytes = (st.Blocks - st.Bfree) * bs
	return nil
}

// ejectVolume unmounts and powers off through udisks, which is what the
// desktop's own eject button does and needs no root for removable drives.
func ejectVolume(target string) error {
	if !sys.Has("udisksctl") {
		return &cli.ExitError{Code: 2, Message: "`disk eject` needs udisksctl (udisks2), which this machine does not have"}
	}
	dev := target
	if !strings.HasPrefix(target, "/dev/") {
		vols, err := listVolumes()
		if err != nil {
			return err
		}
		dev = ""
		for _, v := range vols {
			if v.Mount == strings.TrimRight(target, "/") {
				dev = v.Device
			}
		}
		if dev == "" {
			return fmt.Errorf("no mounted volume at %s", target)
		}
	}
	if _, err := sys.Output("udisksctl", "unmount", "-b", dev); err != nil {
		return err
	}
	_, err := sys.Output("udisksctl", "power-off", "-b", "/dev/"+blockDeviceName(dev))
	return err
}
