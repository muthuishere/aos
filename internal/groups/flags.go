package groups

import (
	"fmt"
	"strconv"
	"strings"
)

// argSet is a minimal flag parser for command arguments. The registry hands
// each command its own argv slice, so commands parse what they declare and
// reject the rest — no global flag state, no init-order surprises.
type argSet struct {
	flags map[string]string
	Rest  []string // positional arguments, in order
}

// parseArgs accepts `--key=value`, `--key value`, and bare `--flag` (which
// records an empty value). valueFlags lists the flags that take a value, so
// that `--copy region` does not swallow the positional after a boolean flag.
//
// A flag with an OPTIONAL value is left out of valueFlags: bare `--wait` then
// records an empty value that String/Int read as "use the fallback", while
// `--wait=5000` still parses through the `=` form.
func parseArgs(args []string, valueFlags ...string) (*argSet, error) {
	takesValue := map[string]bool{}
	for _, name := range valueFlags {
		takesValue[name] = true
	}

	set := &argSet{flags: map[string]string{}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			set.Rest = append(set.Rest, arg)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if name == "" {
			return nil, fmt.Errorf("bad flag %q", arg)
		}
		if !hasValue && takesValue[name] {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--%s needs a value", name)
			}
			i++
			value = args[i]
		}
		set.flags[name] = value
	}
	return set, nil
}

// Has reports whether a flag was given at all.
func (a *argSet) Has(name string) bool {
	_, ok := a.flags[name]
	return ok
}

// String returns a flag's value, or fallback when it was not given.
func (a *argSet) String(name, fallback string) string {
	if value, ok := a.flags[name]; ok && value != "" {
		return value
	}
	return fallback
}

// Int returns a flag's value as an int, or fallback when absent.
func (a *argSet) Int(name string, fallback int) (int, error) {
	value, ok := a.flags[name]
	if !ok || value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("--%s must be a number, got %q", name, value)
	}
	return n, nil
}

// IntPtr returns a pointer to a flag's int value, or nil when it was not given.
// the engine uses *int for "unspecified" throughout, so this maps straight onto
// its option structs.
func (a *argSet) IntPtr(name string) (*int, error) {
	if !a.Has(name) {
		return nil, nil
	}
	n, err := a.Int(name, 0)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// Reject fails on any flag the command did not declare, so a typo surfaces
// instead of being silently ignored.
func (a *argSet) Reject(known ...string) error {
	allowed := map[string]bool{}
	for _, name := range known {
		allowed[name] = true
	}
	for name := range a.flags {
		if !allowed[name] {
			return fmt.Errorf("unknown flag --%s", name)
		}
	}
	return nil
}

// parseInt is strconv.Atoi under a shorter name, kept here so coordinate
// parsing reads cleanly.
func parseInt(value string) (int, error) { return strconv.Atoi(value) }
