package austender

import "testing"

func TestIsFMS(t *testing.T) {
	for _, n := range []string{"FMS ACCOUNT RESERVE BANK OF AUSTRALIA", "FMS ACCOUNT", "fms account reserve bank of australia"} {
		if !IsFMS(n) {
			t.Errorf("IsFMS(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"RESERVE BANK OF AUSTRALIA", "FMS SOLUTIONS PTY LTD", ""} {
		if IsFMS(n) {
			t.Errorf("IsFMS(%q) = true, want false", n)
		}
	}
}
