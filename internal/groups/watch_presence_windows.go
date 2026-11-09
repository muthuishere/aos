package groups

import (
	"fmt"

	"github.com/muthuishere/aos/internal/cli"
	"golang.org/x/sys/windows/registry"
)

const consentStore = `Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\`

func readCameraUse() (deviceUse, error) { return readConsentUse("webcam") }
func readMicUse() (deviceUse, error)    { return readConsentUse("microphone") }

// readConsentUse reads the consent store for one capability. Packaged apps are
// direct subkeys; desktop apps sit under NonPackaged.
func readConsentUse(capability string) (deviceUse, error) {
	root, err := registry.OpenKey(registry.CURRENT_USER, consentStore+capability, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			// Windows creates the key on first use of the capability, so a
			// machine that never used it has nothing to report.
			return deviceUse{}, nil
		}
		return deviceUse{}, &cli.ExitError{Code: 1, Message: fmt.Sprintf("cannot read the %s consent store: %v", capability, err)}
	}
	defer root.Close()

	var entries []consentEntry
	collect := func(parent registry.Key, parentPath string, nonPackaged bool) {
		names, err := parent.ReadSubKeyNames(-1)
		if err != nil {
			return
		}
		for _, name := range names {
			if !nonPackaged && name == "NonPackaged" {
				continue
			}
			k, err := registry.OpenKey(registry.CURRENT_USER, parentPath+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			start, _, _ := k.GetIntegerValue("LastUsedTimeStart")
			stop, _, _ := k.GetIntegerValue("LastUsedTimeStop")
			k.Close()
			entries = append(entries, consentEntry{Key: name, NonPackaged: nonPackaged, Start: start, Stop: stop})
		}
	}
	base := consentStore + capability
	collect(root, base, false)
	if np, err := registry.OpenKey(registry.CURRENT_USER, base+`\NonPackaged`, registry.ENUMERATE_SUB_KEYS); err == nil {
		collect(np, base+`\NonPackaged`, true)
		np.Close()
	}

	apps := consentApps(entries)
	return deviceUse{Busy: len(apps) > 0, Apps: apps}, nil
}
