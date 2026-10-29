// Package website finds and checks suppliers' own websites. A site is linked
// only when the page itself shows it belongs to the supplier: its ABN or ACN,
// or the supplier's name in the page title, site name, copyright line or
// domain. Anything that fails is dropped rather than queued for review.
package website

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// UserAgent identifies the checks to the sites they fetch.
const UserAgent = "spendingexplorer (+https://spending.mchugh.au/methodology#websites)"

// Supplier is what the checks know about a supplier.
type Supplier struct {
	ID       int64
	Name     string
	ABN      string
	Postcode string
	Country  string
}

// Australian reports whether AusTender gives an Australian address.
func (s Supplier) Australian() bool {
	return s.Country == "" || strings.EqualFold(s.Country, "AUSTRALIA")
}

var (
	tradingAs   = regexp.MustCompile(`(?i)\s(t/as?|ta|trading as|atf|as trustee for|formerly:?)\s`)
	trusteeFor  = regexp.MustCompile(`(?i)^\s*(the\s+)?trustees?\s+for\s+`)
	parenthesis = regexp.MustCompile(`\([^)]*\)`)
	nonWord     = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// legal and place words are dropped from the end of a name, so "DT GLOBAL ASIA
// PACIFIC PTY LTD" is matched as "dt global".
var tailWords = setOf(`pty ltd limited proprietary p l inc incorporated llc llp plc corp corporation co company
	gmbh mbh ag kg sa sas sarl srl spa bv nv ab as asa oy oyj kk pte sdn bhd pt tbk
	the trust trustee trustees unit nominees
	australia australian aust aus asia pacific oceania anz nz
	act nsw vic qld wa sa tas nt`)

// generic words are too common to identify a supplier on their own.
var generic = setOf(`global national international australian general united first total advanced integrated
	secure security solutions services service systems technologies technology consulting consultants
	group management digital data health energy defence defense engineering construction
	business professional people partners capital network networks media design project projects
	systems infrastructure environmental resources logistics training medical care community`)

func setOf(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// words splits text into lower-case words, reading "&" as "and".
func words(s string) []string {
	s = strings.ReplaceAll(strings.ToLower(s), "&", " and ")
	return strings.Fields(nonWord.ReplaceAllString(s, " "))
}

// Names returns the forms of a supplier's name to look for: the legal name
// and, for "X T/A Y", the trading name too. Each is a list of words with
// legal forms and place names trimmed from the end.
func Names(name string) [][]string {
	var parts []string
	name = trusteeFor.ReplaceAllString(name, "")
	if loc := tradingAs.FindStringIndex(name); loc != nil {
		parts = []string{name[:loc[0]], name[loc[1]:]}
	} else {
		parts = []string{name}
	}
	var out [][]string
	for _, p := range parts {
		w := words(parenthesis.ReplaceAllString(p, " "))
		if len(w) > 0 && w[0] == "the" {
			w = w[1:]
		}
		for len(w) > 1 && tailWords[w[len(w)-1]] {
			w = w[:len(w)-1]
		}
		if len(w) > 0 && !(len(w) == 1 && tailWords[w[0]]) {
			out = append(out, w)
		}
	}
	return out
}

// distinctive reports whether a word can identify a supplier by itself.
func distinctive(w string) bool {
	return len([]rune(w)) >= 5 && !generic[w]
}

// Match looks for one of names in text. It returns the words matched, or ""
// when there is no match. The whole name always counts; a leading part counts
// when it holds a distinctive word ("austal" for Austal Defence Shipbuilding).
func Match(names [][]string, text string) string { return match(names, text, true) }

// match is Match, with leading parts counting only when prefixes is set.
func match(names [][]string, text string, prefixes bool) string {
	t := " " + strings.Join(words(text), " ") + " "
	compact := strings.ReplaceAll(t, " ", "")
	for _, n := range names {
		for k := len(n); k >= 1; k-- {
			prefix := n[:k]
			if k < len(n) && (!prefixes || !anyDistinctive(prefix)) {
				continue
			}
			phrase := strings.Join(prefix, " ")
			if strings.Contains(t, " "+phrase+" ") {
				return phrase
			}
			// "DT Global" is written "dt-global" or "DTGlobal" in domains and logos.
			if k > 1 && len(strings.Join(prefix, "")) >= 6 && strings.Contains(compact, strings.Join(prefix, "")) {
				return phrase
			}
		}
	}
	return ""
}

// initials returns a name's initials ("kbr" for Kellogg Brown & Root),
// skipping joining words, or "" when there are fewer than three.
func initials(n []string) string {
	var b strings.Builder
	for _, w := range n {
		switch w {
		case "and", "of", "for", "the":
			continue
		}
		b.WriteString(w[:1])
	}
	if b.Len() < 3 {
		return ""
	}
	return b.String()
}

// labelAgrees passes a search answer whose title carries the name's initials
// or first word when the domain starts with the same: "KBR" on kbr.com, "Saab"
// on saab.com, "DOMA Group" on domagroup.com.au. Either alone is too weak.
func labelAgrees(names [][]string, title, label string) string {
	t := " " + strings.Join(words(title), " ") + " "
	label = strings.ReplaceAll(label, "-", "")
	for _, n := range names {
		if in := initials(n); in != "" && strings.Contains(t, " "+in+" ") &&
			(strings.HasPrefix(label, in) || label == strings.Join(n, "")) {
			return in
		}
		if w := n[0]; len(w) >= 3 && !generic[w] && !tailWords[w] && strings.Contains(t, " "+w+" ") && strings.HasPrefix(label, w) {
			return w
		}
	}
	return ""
}

// labelIsName reports whether a domain label reads as the supplier's name:
// the name or its start ("serco" for Serco Citizen Services), the name with
// more after it ("technologyonecorp"), a long part of it ("homeaffairs") or
// its initials ("aemo").
func labelIsName(names [][]string, label string) string {
	label = strings.ReplaceAll(label, "-", "")
	for _, n := range names {
		full := strings.Join(n, "")
		switch {
		case len(label) >= 4 && (strings.HasPrefix(full, label) || strings.HasPrefix(label, full)),
			len(label) >= 8 && strings.Contains(full, label),
			label == initials(n):
			return strings.Join(n, " ")
		}
	}
	return ""
}

func anyDistinctive(ws []string) bool {
	for _, w := range ws {
		if distinctive(w) {
			return true
		}
	}
	return false
}

// Candidates are domains built from the supplier's name, most likely first.
func Candidates(s Supplier) []string {
	tlds := []string{".com.au", ".com"}
	if !s.Australian() {
		tlds = []string{".com"}
	}
	seen := map[string]bool{}
	var labels []string
	add := func(l string) {
		if len(l) >= 3 && len(l) <= 40 && !seen[l] {
			seen[l] = true
			labels = append(labels, l)
		}
	}
	for _, n := range Names(s.Name) {
		add(strings.Join(n, ""))
		if len(n) > 1 {
			add(strings.Join(n, "-"))
		}
		if len(n) > 2 {
			add(n[0] + n[1])
		}
		if len(n) > 1 && distinctive(n[0]) {
			add(n[0])
		}
	}
	var out []string
	for _, l := range labels {
		for _, t := range tlds {
			out = append(out, l+t)
		}
	}
	return out
}

// Result is the outcome of checking one site.
type Result struct {
	URL      string // the page reached, after redirects
	Passed   bool
	Evidence string // what showed the site is the supplier's
	Detail   string // why it failed
	Blocked  bool   // the site answered but refused the check (HTTP 401, 403, 429, 503)
	Reached  bool   // the site answered at all
}

// Page is what Check reads from a fetched page.
type Page struct {
	URL   *url.URL
	Title string // <title>, og:site_name, og:title and application-name
	Text  string // the page with tags removed
}

var (
	titleTag  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	metaTag   = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	metaName  = regexp.MustCompile(`(?is)\b(?:property|name)\s*=\s*["']?(og:site_name|og:title|application-name)["'\s>]`)
	metaValue = regexp.MustCompile(`(?is)\bcontent\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	scripts   = regexp.MustCompile(`(?is)<(script|style|noscript)\b.*?</(script|style|noscript)>`)
	tags      = regexp.MustCompile(`(?s)<[^>]*>`)
	copyright = regexp.MustCompile(`(?i)(?:©|&copy;|copyright)(.{0,100})`)
)

// ParsePage pulls the title, site name and text out of an HTML page.
func ParsePage(u *url.URL, body string) Page {
	p := Page{URL: u}
	var titles []string
	if m := titleTag.FindStringSubmatch(body); m != nil {
		titles = append(titles, m[1])
	}
	for _, tag := range metaTag.FindAllString(body, -1) {
		if metaName.MatchString(tag) {
			if v := metaValue.FindStringSubmatch(tag); v != nil {
				titles = append(titles, v[1]+v[2])
			}
		}
	}
	p.Title = html.UnescapeString(strings.Join(titles, " | "))
	text := tags.ReplaceAllString(scripts.ReplaceAllString(body, " "), " ")
	p.Text = strings.Join(strings.Fields(html.UnescapeString(text)), " ")
	return p
}

// numberPattern matches digits written with or without spaces between groups.
func numberPattern(groups ...string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\D)` + strings.Join(groups, `[\s\x{00a0}]?`) + `(?:\D|$)`)
}

// idOnPage reports whether the page shows the supplier's ABN or ACN.
func idOnPage(abn, text string) string {
	if len(abn) != 11 {
		return ""
	}
	if numberPattern(abn[0:2], abn[2:5], abn[5:8], abn[8:11]).MatchString(text) {
		return "ABN on site"
	}
	// A company's ACN is the last nine digits of its ABN.
	if numberPattern(abn[2:5], abn[5:8], abn[8:11]).MatchString(text) && strings.Contains(strings.ToUpper(text), "ACN") {
		return "ACN on site"
	}
	return ""
}

// australianSign reports whether a page reads as an Australian business's.
func australianSign(s Supplier, p Page) bool {
	if strings.HasSuffix(p.URL.Hostname(), ".au") {
		return true
	}
	return len(s.Postcode) == 4 && numberPattern(s.Postcode).MatchString(p.Text)
}

var placeholderText = regexp.MustCompile(`(?i)\b(?:launching soon|coming soon|under construction|domain (?:name )?(?:is |may be )?for sale|buy this domain|parked (?:free|domain))\b`)

// placeholder reports whether a page is a short holding page: coming soon,
// under construction or a domain for sale.
func placeholder(p Page) bool {
	return len(p.Text) < 3000 && placeholderText.MatchString(p.Title+" "+p.Text)
}

// Strict is the bar for a domain guessed from the name, where a different
// business of the same name is a real risk. Search answers come from a model
// that was told the ABN and location, and use the plain bar.
type Bar int

const (
	Plain Bar = iota
	Strict
)

// Check decides whether a fetched page is the supplier's site.
func Check(s Supplier, p Page, bar Bar) Result {
	r := Result{URL: p.URL.String()}
	if ev := idOnPage(s.ABN, p.Text); ev != "" {
		r.Passed, r.Evidence = true, ev
		return r
	}
	if placeholder(p) {
		r.Detail = "placeholder page"
		return r
	}
	names := Names(s.Name)
	host := strings.TrimPrefix(p.URL.Hostname(), "www.")
	label := host
	if i := strings.Index(label, "."); i > 0 {
		label = label[:i]
	}
	// A guessed domain must carry the whole name in its title or copyright
	// line: a first word alone ("Riverina", "Universal") or a domain alone (a
	// parked domain for sale) is too weak. A page that names only its own
	// domain ("ministryofhealth.com.au") says nothing either.
	own := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(host))
	sources := []struct{ where, text string }{
		{"title", own.ReplaceAllString(p.Title, " ")},
		{"copyright line", own.ReplaceAllString(strings.Join(copyright.FindAllString(p.Text, 5), " "), " ")},
	}
	if bar == Plain {
		sources = append(sources, struct{ where, text string }{"domain", label})
	}
	var where, matched string
	for _, src := range sources {
		if m := match(names, src.text, bar == Plain); m != "" {
			where, matched = src.where, m
			break
		}
	}
	if matched == "" && bar == Plain {
		if m := labelAgrees(names, sources[0].text, label); m != "" {
			where, matched = "title and domain", m
		}
	}
	if matched == "" {
		r.Detail = "name not in title, copyright line or domain"
		return r
	}
	if bar == Strict {
		single := true
		for _, n := range names {
			if len(n) > 1 {
				single = false
			}
		}
		switch {
		case single && !(len(s.Postcode) == 4 && numberPattern(s.Postcode).MatchString(p.Text)):
			r.Detail = fmt.Sprintf("one-word name %q matched in %s, but no ABN, ACN or postcode on the page", matched, where)
			return r
		case s.Australian() && !australianSign(s, p) && !strings.Contains(strings.ToLower(p.Text), "australia"):
			r.Detail = fmt.Sprintf("%q matched in %s, but nothing on the page says Australia", matched, where)
			return r
		}
	}
	r.Passed, r.Evidence = true, fmt.Sprintf("name in %s: %q", where, matched)
	return r
}

// Excluded hosts are directories, registers, social media and the like,
// which mention suppliers but are not their sites.
var excluded = []string{"linkedin.com", "facebook.com", "instagram.com", "twitter.com", "x.com", "youtube.com",
	"wikipedia.org", "wikidata.org", "abr.business.gov.au", "abn.business.gov.au", "asic.gov.au", "tenders.gov.au",
	"grants.gov.au", "bloomberg.com", "dnb.com", "zoominfo.com", "crunchbase.com", "opencorporates.com",
	"yellowpages.com.au", "truelocal.com.au", "hotfrog.com.au", "glassdoor.com", "seek.com.au", "indeed.com",
	"google.com", "bing.com", "apple.com", "spending.mchugh.au", "abnlookup.net", "companieslist.co"}

// Excluded reports whether a host is one that can't be a supplier's own site.
func Excluded(host string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	for _, e := range excluded {
		if host == e || strings.HasSuffix(host, "."+e) {
			return true
		}
	}
	return false
}

// Checker fetches pages for Check.
type Checker struct {
	Client *http.Client
}

// NewChecker returns a Checker with sensible timeouts. It speaks HTTP/1.1
// only: some CDNs reset Go's HTTP/2 streams (DHL, HPE, Optus).
func NewChecker() *Checker {
	t := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSClientConfig:     &tls.Config{NextProtos: []string{"http/1.1"}},
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: 2,
	}
	return &Checker{Client: &http.Client{Timeout: 20 * time.Second, Transport: t}}
}

// Fetch reads a page, up to 2 MB. A missing domain is reported as such, so
// callers can tell a dead guess from a refusal.
func (c *Checker) Fetch(ctx context.Context, raw string) (Page, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Page{}, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Page{}, 0, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-AU,en;q=0.8")
	resp, err := c.Client.Do(req)
	if err != nil {
		return Page{}, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return Page{}, resp.StatusCode, err
	}
	return ParsePage(resp.Request.URL, string(body)), resp.StatusCode, nil
}

// Visit fetches a site and checks it. Plain-bar answers whose site refuses
// automated visits pass when the domain itself carries the full name.
func (c *Checker) Visit(ctx context.Context, s Supplier, raw string, bar Bar) Result {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return Result{URL: raw, Detail: "not a web address"}
	}
	if Excluded(u.Hostname()) {
		return Result{URL: raw, Detail: "a directory or social media site, not the supplier's own"}
	}
	p, status, err := c.Fetch(ctx, raw)
	if err != nil {
		var dns *net.DNSError
		if errors.As(err, &dns) && dns.IsNotFound {
			return Result{URL: raw, Detail: "no such domain"}
		}
		return Result{URL: raw, Detail: shortErr(err)}
	}
	r := Check(s, p, bar)
	r.Reached = true
	switch {
	case status == 401 || status == 403 || status == 429 || status == 503:
		r = Result{URL: p.URL.String(), Reached: true, Blocked: true, Detail: fmt.Sprintf("HTTP %d", status)}
		if bar == Plain {
			label := strings.TrimPrefix(p.URL.Hostname(), "www.")
			label = label[:max(strings.Index(label, "."), 0)]
			if m := labelIsName(Names(s.Name), label); m != "" {
				r.Passed, r.Evidence = true, fmt.Sprintf("site refused the check; domain reads as the name %q", m)
			}
		}
		return r
	case status >= 400:
		return Result{URL: p.URL.String(), Reached: true, Detail: fmt.Sprintf("HTTP %d", status)}
	case Excluded(p.URL.Hostname()):
		return Result{URL: p.URL.String(), Reached: true, Detail: "redirects to a directory or social media site"}
	}
	return r
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s) > 120 {
		s = s[i+2:]
	}
	return strings.TrimFunc(s, unicode.IsSpace)
}

// errorPath is where some sites send a missing page while still answering
// HTTP 200; the site's home page is linked instead.
var errorPath = regexp.MustCompile(`(?i)(not-?found|/404|/error)`)

// Home trims a checked page's address to what the site links: scheme, host
// and path, without query or fragment.
func Home(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
	}
	if u.Path == "" || strings.HasPrefix(u.Path, "//") || errorPath.MatchString(u.Path) {
		u.Path = "/" + strings.TrimLeft(u.Path, "/")
		if errorPath.MatchString(u.Path) {
			u.Path = "/"
		}
	}
	u.RawPath = ""
	return u.String()
}
