package austender

import "testing"

func TestNameKey(t *testing.T) {
	for in, want := range map[string]string{
		"The Treasury": "treasury",
		"Department of Health, Disability & Ageing": "department of health disability and ageing",
		"  Attorney-General's Department ":          "attorney general s department",
	} {
		if got := NameKey(in); got != want {
			t.Errorf("NameKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSupplierKey(t *testing.T) {
	if got := SupplierKey("Acme Pty Ltd", "97109189059"); got != "abn:97109189059" {
		t.Errorf("with ABN: %s", got)
	}
	if a, b := SupplierKey("ACME PTY LTD", ""), SupplierKey("Acme Limited", ""); a != b {
		t.Errorf("suffixes not removed: %s vs %s", a, b)
	}
}

func TestFiscalYear(t *testing.T) {
	for in, want := range map[string]string{"2026-07-01": "2026-27", "2026-06-30": "2025-26", "2099-12-31": "2099-00"} {
		if got := FiscalYear(in); got != want {
			t.Errorf("FiscalYear(%s) = %s, want %s", in, got, want)
		}
	}
}
