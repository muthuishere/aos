package groups

import (
	"fmt"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
	"github.com/muthuishere/aos/internal/sys"
)

func xrandrOutputs() ([]xrandrOutput, error) {
	if !sys.Has("xrandr") {
		return nil, &cli.ExitError{Code: 2, Message: "display modes need xrandr (X11), which this machine does not have"}
	}
	out, err := sys.Output("xrandr", "--query")
	if err != nil {
		return nil, err
	}
	return parseXrandr(out), nil
}

func xrandrFor(mon engine.Monitor) (xrandrOutput, error) {
	outs, err := xrandrOutputs()
	if err != nil {
		return xrandrOutput{}, err
	}
	for _, o := range outs {
		if o.X == mon.X && o.Y == mon.Y {
			return o, nil
		}
	}
	return xrandrOutput{}, fmt.Errorf("no xrandr output at %d,%d", mon.X, mon.Y)
}

func listDisplayModes(mon engine.Monitor) ([]displayMode, error) {
	o, err := xrandrFor(mon)
	return o.Modes, err
}

func setDisplayMode(mon engine.Monitor, m displayMode) error {
	o, err := xrandrFor(mon)
	if err != nil {
		return err
	}
	_, err = sys.Output("xrandr", "--output", o.Name, "--mode", fmt.Sprintf("%dx%d", m.Width, m.Height), "--rate", fmt.Sprintf("%.2f", m.Refresh))
	return err
}

// setMirror puts every other connected output on the first one (on), or
// lays them out left to right (off).
func setMirror(on bool) error {
	outs, err := xrandrOutputs()
	if err != nil {
		return err
	}
	if len(outs) < 2 {
		return fmt.Errorf("mirroring needs two monitors")
	}
	for i, o := range outs[1:] {
		args := []string{"--output", o.Name, "--auto"}
		if on {
			args = append(args, "--same-as", outs[0].Name)
		} else {
			args = append(args, "--right-of", outs[i].Name)
		}
		if _, err := sys.Output("xrandr", args...); err != nil {
			return err
		}
	}
	return nil
}
