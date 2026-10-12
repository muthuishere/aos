package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// Zone is a logical region within a monitor that resolves to a Rect when
// applied to a specific monitor. Supported forms are the enum zones
// (1A, 1B, 2A, 2B, 2C, 2D) and split zones in N:M notation.
type Zone interface {
	Rect(m Monitor) Rect
	String() string
}

// ParseZone parses an enum zone or a split zone string.
//
// Enum zones (case-insensitive): 1A, 1B, 2A, 2B, 2C, 2D.
// Split zones: N:M with N >= 1, 1 <= M <= N — divide the monitor width into
// N equal columns and select column M (1-indexed).
func ParseZone(s string) (Zone, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("zone: empty string")
	}
	if strings.Contains(s, ":") {
		return parseSplit(s)
	}
	return parseEnum(s)
}

type enumZone string

func parseEnum(s string) (Zone, error) {
	upper := strings.ToUpper(s)
	switch upper {
	case "1A", "1B", "2A", "2B", "2C", "2D":
		return enumZone(upper), nil
	}
	return nil, fmt.Errorf("zone: invalid enum %q (valid: 1A, 1B, 2A, 2B, 2C, 2D)", s)
}

func (z enumZone) String() string { return string(z) }

func (z enumZone) Rect(m Monitor) Rect {
	halfW := m.Width / 2
	halfH := m.Height / 2
	switch z {
	case "1A":
		return Rect{X: m.X, Y: m.Y, W: halfW, H: m.Height}
	case "1B":
		return Rect{X: m.X + halfW, Y: m.Y, W: m.Width - halfW, H: m.Height}
	case "2A":
		return Rect{X: m.X, Y: m.Y, W: halfW, H: halfH}
	case "2B":
		return Rect{X: m.X + halfW, Y: m.Y, W: m.Width - halfW, H: halfH}
	case "2C":
		return Rect{X: m.X, Y: m.Y + halfH, W: halfW, H: m.Height - halfH}
	case "2D":
		return Rect{X: m.X + halfW, Y: m.Y + halfH, W: m.Width - halfW, H: m.Height - halfH}
	}
	return Rect{}
}

type splitZone struct {
	n, m int
}

// parseSplit reads a split zone, accepting either order of the two numbers.
//
// "the M-th of N" is how people say it out loud, so 1:2 is the left half and
// 3:1 is the left third — and both readings can be supported without ambiguity.
// A split has 1 <= M <= N, so in one ordering the first number is never smaller
// than the second, and in the other it is never larger. The only strings both
// readings accept are those where the numbers are equal (2:2, 3:3), and there
// the two readings mean the same rectangle. So the larger number is N.
func parseSplit(s string) (Zone, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("zone: invalid split %q (expected N:M or M:N, such as 1:2 or 2:1)", s)
	}
	first, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || first <= 0 {
		return nil, fmt.Errorf("zone: invalid split %q: both numbers must be positive integers", s)
	}
	second, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || second <= 0 {
		return nil, fmt.Errorf("zone: invalid split %q: both numbers must be positive integers", s)
	}

	n, m := first, second
	if second > first {
		n, m = second, first
	}
	return splitZone{n: n, m: m}, nil
}

// String renders the spoken order — the M-th of N — because that is the form a
// person types and the form the CLI prints back.
func (z splitZone) String() string { return fmt.Sprintf("%d:%d", z.m, z.n) }

func (z splitZone) Rect(mon Monitor) Rect {
	cell := mon.Width / z.n
	return Rect{
		X: mon.X + (z.m-1)*cell,
		Y: mon.Y,
		W: cell,
		H: mon.Height,
	}
}
