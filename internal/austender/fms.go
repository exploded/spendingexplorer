package austender

import "strings"

// IsFMS reports whether a supplier name is the account Defence pays for US
// Foreign Military Sales cases. AusTender publishes it as "FMS ACCOUNT RESERVE
// BANK OF AUSTRALIA" (once as plain "FMS ACCOUNT"); the US Government, not a
// company, is the real supplier.
func IsFMS(name string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(name)), "FMS ACCOUNT")
}
