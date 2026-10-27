package austender

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// NameKey normalises an organisation name for matching: lower case, "&" as
// "and", punctuation dropped, a leading "the" removed and spaces collapsed.
func NameKey(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "&", " and "))
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, s)
	f := strings.Fields(s)
	if len(f) > 0 && f[0] == "the" {
		f = f[1:]
	}
	return strings.Join(f, " ")
}

var companySuffixes = []string{" pty ltd", " pty limited", " proprietary limited", " limited", " ltd", " inc", " llc", " plc"}

// SupplierKey identifies a supplier by ABN, or by name without company
// suffixes when no ABN is published.
func SupplierKey(name, abn string) string {
	if abn != "" {
		return "abn:" + abn
	}
	k := NameKey(name)
	for _, suf := range companySuffixes {
		k = strings.TrimSuffix(k, suf)
	}
	return "name:" + k
}

// FiscalYear returns the Australian financial year (July to June) of a
// YYYY-MM-DD date, e.g. 2026-07-01 -> 2026-27.
func FiscalYear(date string) string {
	if len(date) < 7 {
		return ""
	}
	y, _ := strconv.Atoi(date[:4])
	if m, _ := strconv.Atoi(date[5:7]); m < 7 {
		y--
	}
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}
