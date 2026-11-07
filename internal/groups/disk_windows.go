package groups

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

const (
	driveRemovable = 2
	driveFixed     = 3
	driveRemote    = 4
	driveCDROM     = 5

	fsctlLockVolume         = 0x00090018
	fsctlDismountVolume     = 0x00090020
	ioctlStorageMediaRemove = 0x002D4804
	ioctlStorageEjectMedia  = 0x002D4808
)

func listVolumes() ([]volumeInfo, error) {
	buf := make([]uint16, 512)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil {
		return nil, err
	}
	var out []volumeInfo
	for _, root := range splitMultiSZ(buf[:n]) {
		v, err := windowsVolume(root)
		if err != nil {
			continue // an empty card reader or a disconnected share
		}
		out = append(out, v)
	}
	return out, nil
}

// splitMultiSZ splits a double-NUL-terminated UTF-16 list ("C:\\\0D:\\\0\0").
func splitMultiSZ(buf []uint16) []string {
	var out []string
	start := 0
	for i, ch := range buf {
		if ch == 0 {
			if i > start {
				out = append(out, windows.UTF16ToString(buf[start:i]))
			}
			start = i + 1
		}
	}
	return out
}

func windowsVolume(root string) (volumeInfo, error) {
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return volumeInfo{}, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return volumeInfo{}, err
	}
	v := volumeInfo{Mount: root, TotalBytes: total, FreeBytes: avail, UsedBytes: total - free}
	label := make([]uint16, windows.MAX_PATH+1)
	fs := make([]uint16, windows.MAX_PATH+1)
	if windows.GetVolumeInformation(p, &label[0], uint32(len(label)), nil, nil, nil, &fs[0], uint32(len(fs))) == nil {
		v.Label = windows.UTF16ToString(label)
		v.FSType = windows.UTF16ToString(fs)
	}
	switch windows.GetDriveType(p) {
	case driveRemovable, driveCDROM:
		v.Removable = true
	}
	return v, nil
}

func volumeOf(path string) (volumeInfo, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return volumeInfo{}, err
	}
	root := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &root[0], uint32(len(root))); err != nil {
		return volumeInfo{}, err
	}
	return windowsVolume(windows.UTF16ToString(root))
}

// ejectVolume does what Explorer's Eject does for a removable drive: lock
// and dismount the volume so nothing writes to it, allow removal, eject.
func ejectVolume(target string) error {
	letter := strings.ToUpper(strings.TrimRight(target, `\/`))
	if len(letter) != 2 || letter[1] != ':' {
		return fmt.Errorf("give a drive letter, e.g. E:")
	}
	root, _ := windows.UTF16PtrFromString(letter + `\`)
	if t := windows.GetDriveType(root); t != driveRemovable && t != driveCDROM {
		return fmt.Errorf("%s is not a removable drive", letter)
	}
	path, _ := windows.UTF16PtrFromString(`\\.\` + letter)
	h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var n uint32
	if err := windows.DeviceIoControl(h, fsctlLockVolume, nil, 0, nil, 0, &n, nil); err != nil {
		return fmt.Errorf("%s is in use; close what is using it and try again", letter)
	}
	if err := windows.DeviceIoControl(h, fsctlDismountVolume, nil, 0, nil, 0, &n, nil); err != nil {
		return err
	}
	prevent := byte(0)
	_ = windows.DeviceIoControl(h, ioctlStorageMediaRemove, &prevent, 1, nil, 0, &n, nil)
	return windows.DeviceIoControl(h, ioctlStorageEjectMedia, nil, 0, nil, 0, &n, nil)
}
