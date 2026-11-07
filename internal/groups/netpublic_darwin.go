package groups

import "github.com/muthuishere/aos/internal/sys"

func listVPNs() ([]vpnConn, error) {
	out, err := sys.Output("scutil", "--nc", "list")
	if err != nil {
		return nil, err
	}
	return parseScutilNC(out), nil
}

func switchVPN(name string, on bool) error {
	verb := "stop"
	if on {
		verb = "start"
	}
	_, err := sys.Output("scutil", "--nc", verb, name)
	return err
}
