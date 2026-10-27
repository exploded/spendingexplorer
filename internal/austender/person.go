package austender

import (
	"regexp"
	"strings"
)

// orgWord marks a name as an organisation's: legal forms here and overseas,
// and words people's names do not contain.
var orgWord = regexp.MustCompile(`(?i)\b(PTY|PROPRIETARY|LTD|LIMITED|INC|INCORPORATED|CORP|CORPORATION|COMPANY|CO|PLC|LLC|LLP|L\.?P|PTE|GMBH|MBH|AG|KG|SA|SAS|SARL|SAL|SRL|SPA|BV|NV|AB|AS|ASA|OY|OYJ|KK|SDN|BHD|PT|TBK|WLL|CTY|TNHH|TRUST|TRUSTEE|TRUSTEES|UNIVERSITY|COLLEGE|SCHOOL|INSTITUTE|COUNCIL|DEPARTMENT|GOVERNMENT|MINISTRY|EMBASSY|AUTHORITY|COMMISSION|AGENCY|OFFICE|ASSOCIATION|SOCIETY|FOUNDATION|CLUB|UNION|NETWORK|GROUP|HOLDINGS|PARTNERS|SERVICES|SOLUTIONS|CONSULTING|CONSULTANTS|CONSULTANCY|SYSTEMS|TECHNOLOGIES|TECHNOLOGY|INTERNATIONAL|AUSTRALIA|AUSTRALIAN|ENTERPRISES|INDUSTRIES|HOSPITAL|HEALTH|CENTRE|CENTER|MEDIA|MANAGEMENT|LAWYERS|CHAMBERS|ARCHITECTS|ENGINEERS|BOARD|NATIONAL|PROJECT|PROGRAM|PROGRAMME|ACCOUNT|FUND|BANK|FORCE|ORGANISATION|ORGANIZATION|COMMUNITY|EVENTS|SOFTWARE|SHIPYARD|SHIPYARDS|DOCKYARD)\b|\bA/S\b|&|@|\d`)

// tradingAs splits "<owner> trading as <business name>".
var tradingAs = regexp.MustCompile(`(?i)\s(t/as?|ta|trading as|atf)\s`)

var (
	titles = map[string]bool{"MR": true, "MRS": true, "MS": true, "MISS": true, "DR": true, "PROF": true,
		"PROFESSOR": true, "SIR": true, "DAME": true, "HON": true}
	postNominals = map[string]bool{"SC": true, "KC": true, "QC": true, "AM": true, "AO": true, "AC": true,
		"OAM": true, "PSM": true, "PHD": true, "BARRISTER": true, "BARRISTER-AT-LAW": true}
	nameToken = regexp.MustCompile(`^[A-Z][A-Z'’-]*$`)
)

// LooksLikePerson reports whether a supplier name reads as a person's name:
// two to four words, no organisation words, and a known given name or an
// initial first (or after the comma in "Surname, Given"). given holds upper-
// case given names. It is for suppliers with no ABR record; the ABR's entity
// type decides for everyone else.
func LooksLikePerson(name string, given map[string]bool) bool {
	if loc := tradingAs.FindStringIndex(name); loc != nil {
		name = name[:loc[0]]
	}
	if orgWord.MatchString(strings.ReplaceAll(name, ".", "")) { // S.A.S., B.V.
		return false
	}
	surnameFirst := strings.Count(name, ",") == 1
	var words []string
	for _, w := range strings.Fields(strings.ToUpper(strings.ReplaceAll(name, ",", " , "))) {
		w = strings.Trim(w, ".")
		if w == "" || titles[w] || postNominals[w] {
			continue
		}
		words = append(words, w)
	}
	first := ""
	if surnameFirst {
		for i, w := range words {
			if w == "," && i+1 < len(words) {
				first = words[i+1]
			}
		}
		words = removeComma(words)
	} else if len(words) > 0 {
		first = words[0]
	}
	if len(words) < 2 || len(words) > 4 {
		return false
	}
	for _, w := range words {
		if !nameToken.MatchString(strings.ReplaceAll(w, ".", "")) {
			return false
		}
	}
	return first != "THE" && (given[first] || isInitials(first))
}

// isInitials reports whether w is one or two initials, such as "J" or "JN".
func isInitials(w string) bool {
	w = strings.ReplaceAll(w, ".", "")
	return len(w) >= 1 && len(w) <= 2 && nameToken.MatchString(w)
}

func removeComma(words []string) []string {
	out := words[:0:0]
	for _, w := range words {
		if w != "," {
			out = append(out, w)
		}
	}
	return out
}
