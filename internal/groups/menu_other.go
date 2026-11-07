//go:build !darwin

package groups

// The menu group is declared for darwin; dispatch exits 2 elsewhere. A
// Windows UIA backend (MenuBar/MenuItem Expand+Invoke) is a follow-up.
func listMenu(string) ([]menuItem, error) { return nil, errUnsupported }
func pressMenu(string, string) error      { return errUnsupported }
