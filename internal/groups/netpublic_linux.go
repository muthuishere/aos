package groups

import (
	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

func needNmcli() error {
	if !sys.Has("nmcli") {
		return &cli.ExitError{Code: 2, Message: "VPN control needs NetworkManager's nmcli, which this machine does not have"}
	}
	return nil
}

func listVPNs() ([]vpnConn, error) {
	if err := needNmcli(); err != nil {
		return nil, err
	}
	out, err := sys.Output("nmcli", "-t", "-f", "NAME,TYPE,STATE", "connection", "show")
	if err != nil {
		return nil, err
	}
	return parseNmcliVPN(out), nil
}

func switchVPN(name string, on bool) error {
	if err := needNmcli(); err != nil {
		return err
	}
	verb := "down"
	if on {
		verb = "up"
	}
	_, err := sys.Output("nmcli", "connection", verb, "id", name)
	return err
}
