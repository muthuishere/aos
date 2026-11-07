package groups

import "github.com/muthuishere/aos/internal/sys"

// listVPNs uses rasdial, which reports only the connected entries; Windows
// keeps the configured-but-idle list in the phonebook, not on any command.
func listVPNs() ([]vpnConn, error) {
	out, err := sys.Output("rasdial")
	if err != nil {
		return nil, err
	}
	return parseRasdial(out), nil
}

// switchVPN dials a saved entry with its saved credentials, or hangs it up.
func switchVPN(name string, on bool) error {
	args := []string{name}
	if !on {
		args = append(args, "/disconnect")
	}
	_, err := sys.Output("rasdial", args...)
	return err
}
