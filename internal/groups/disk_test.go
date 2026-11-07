package groups

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

func TestParseProcMounts(t *testing.T) {
	got := parseProcMounts(`sysfs /sys sysfs rw 0 0
/dev/nvme0n1p2 / ext4 rw,relatime 0 0
/dev/nvme0n1p1 /boot/efi vfat rw 0 0
/dev/loop3 /snap/core/1 squashfs ro 0 0
/dev/sdb1 /media/me/My\040USB vfat rw 0 0
/dev/nvme0n1p2 / ext4 rw 0 0
tmpfs /run tmpfs rw 0 0
`)
	if len(got) != 2 {
		t.Fatalf("want 2 real mounts, got %+v", got)
	}
	if got[1].Mount != "/media/me/My USB" || got[1].Device != "/dev/sdb1" || got[1].FSType != "vfat" {
		t.Errorf("escaped mount: %+v", got[1])
	}
}

func TestBlockDeviceName(t *testing.T) {
	for in, want := range map[string]string{
		"/dev/sdb1": "sdb", "/dev/sda": "sda", "/dev/nvme0n1p2": "nvme0n1",
		"/dev/nvme0n1": "nvme0n1", "/dev/mmcblk0p1": "mmcblk0",
	} {
		if got := blockDeviceName(in); got != want {
			t.Errorf("blockDeviceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlistRemovable(t *testing.T) {
	fixed := "<key>Ejectable</key>\n\t<false/>\n\t<key>Removable</key>\n\t<false/>"
	usb := "<key>Ejectable</key>\n\t<true/>\n\t<key>Removable</key>\n\t<false/>"
	if plistRemovable(fixed) || !plistRemovable(usb) {
		t.Error("plistRemovable misread diskutil")
	}
}

func TestIsSystemVolume(t *testing.T) {
	for _, s := range []string{"/", "C:", `c:\`, "/System/Volumes/Data"} {
		if !isSystemVolume(s) {
			t.Errorf("%q should be refused", s)
		}
	}
	for _, s := range []string{"/Volumes/USB", "E:", "/dev/sdb1"} {
		if isSystemVolume(s) {
			t.Errorf("%q should be allowed", s)
		}
	}
}

func TestVolumeFinish(t *testing.T) {
	v := volumeInfo{TotalBytes: 1000, FreeBytes: 250}
	v.finish()
	if v.UsedBytes != 750 || v.UsedPercent != 75 {
		t.Errorf("got %+v", v)
	}
}

func TestDiskLiveJSON(t *testing.T) {
	var out bytes.Buffer
	if err := runDiskList(&cli.Ctx{Stdout: &out}, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var vols []volumeInfo
	if err := json.Unmarshal(out.Bytes(), &vols); err != nil || len(vols) == 0 {
		t.Fatalf("no volumes: %v %s", err, out.String())
	}
	out.Reset()
	if err := runDiskUsage(&cli.Ctx{Stdout: &out}, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var v volumeInfo
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v.TotalBytes == 0 || v.FreeBytes > v.TotalBytes {
		t.Errorf("implausible home volume: %+v %v", v, err)
	}
}
