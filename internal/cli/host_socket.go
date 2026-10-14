package cli

import (
	"net"
	"time"
)

// pingSocket checks that something listens on the socket.
func pingSocket(socket string) error {
	conn, err := net.DialTimeout("unix", socket, 3*time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

// runOnSocket is the SSH host path, which this build does not carry yet.
func (c *Ctx) runOnSocket(host Host, cmd *Command, words, rest []string) int {
	c.Warnf("aos: %q cannot be sent to %s: socket hosts are not supported by this build\n", cmd.Route(), host.Socket)
	c.Warnf("Fix: pass --host=local to act on this machine.\n")
	return 2
}
