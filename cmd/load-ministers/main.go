// Command load-ministers loads federal ministers from the Parliamentary
// Handbook, which the Parliamentary Library publishes as an API:
//
//	https://handbookapi.aph.gov.au/api/
//
// Wikidata (CC0), matched on the Handbook ID, adds each person's Wikipedia
// article and a Wikimedia Commons photo, kept with its licence and credit.
// data/minister_titles.csv places each ministerial title in a portfolio as
// named in the latest Budget; the Handbook doesn't say which portfolio a
// title belongs to.
//
//	load-ministers -fetch   # refresh the snapshots in data/raw and the photo cache, then load
//	load-ministers          # load the snapshots
//
// Photos are cached in .local/people and are not committed. Anything the
// load can't place is logged and noted in load_run, not queued for review: a
// title missing from the CSV, a CSV portfolio the latest Budget doesn't have,
// or a portfolio with no current minister.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/source"
)

const (
	ministriesFile  = "aph-ministries.json"
	recordsFile     = "aph-ministry-records.json"
	individualsFile = "aph-individuals.json"
	wikidataFile    = "wikidata-ministers.json"
	commonsFile     = "commons-photos.json"
	titlesCSV       = "data/minister_titles.csv"
)

// firstMinistry is the earliest ministry loaded: the second Morrison
// ministry, which the Handbook dates from the election on 18 May 2019.
// data/minister_titles.csv places every title held since then.
const firstMinistry = 73

type ministry struct {
	MID       int
	Name      string
	DateStart string
	DateEnd   string
}

// record is one appointment in one ministry. An appointment that runs across
// several ministries appears in each, with the same dates.
type record struct {
	MID         int
	PHID        string
	DisplayName string
	Role        string
	Prep        string
	Entity      string
	RDateStart  string
	RDateEnd    string
	Ministry    string
}

// title is the appointment as styled, e.g. "Minister for Health and Ageing".
func (r record) title() string {
	var parts []string
	for _, s := range []string{r.Role, r.Prep, r.Entity} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

type individual struct {
	PHID                string
	GivenName           string
	PreferredName       string
	FamilyName          string
	DisplayName         string
	Party               string
	Electorate          string
	State               string
	MPorSenator         []string
	InCurrentParliament string
	ServiceStart        string `json:"ServiceHistory_Start"`
	ServiceEnd          string `json:"ServiceHistory_End"`
}

type wikidataPerson struct {
	PHID      string
	Item      string
	Label     string
	Wikipedia string
	Image     string // Commons file name
}

type photo struct {
	File       string
	ThumbURL   string
	Type       string
	Page       string
	Licence    string
	LicenceURL string
	Credit     string
}

// cacheName is the photo's file name in the cache.
func (p photo) cacheName() string {
	h := sha256.Sum256([]byte(p.File))
	ext := ".jpg"
	switch p.Type {
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	}
	return hex.EncodeToString(h[:8]) + ext
}

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	raw := flag.String("raw", "data/raw", "folder holding the snapshots")
	cache := flag.String("cache", ".local/people", "photo cache folder")
	refresh := flag.Bool("fetch", false, "download fresh snapshots and photos first")
	flag.Parse()
	ctx := context.Background()

	if *refresh {
		if err := fetch(ctx, *raw, *cache, firstMinistry); err != nil {
			log.Fatal(err)
		}
	}
	if err := load(ctx, *dbPath, *raw, *cache); err != nil {
		log.Fatal(err)
	}
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// holdingKey identifies an appointment across the ministries it spans.
type holdingKey struct{ phid, title, start string }

type holding struct {
	role, end, ministry string
	mid                 int
}

// holdings merges the records of ministries from first on into one
// appointment each, named after the latest ministry it was part of.
func holdings(records []record, first int) map[holdingKey]holding {
	out := map[holdingKey]holding{}
	for _, r := range records {
		if r.MID < first {
			continue
		}
		k := holdingKey{r.PHID, r.title(), r.RDateStart}
		if h, ok := out[k]; ok && h.mid > r.MID {
			continue
		}
		out[k] = holding{role: r.Role, end: r.RDateEnd, ministry: r.Ministry, mid: r.MID}
	}
	return out
}

// casedName turns the Handbook's capitalised family name ("McALLISTER",
// "O'NEIL") into "McAllister" and "O'Neil". It is only a fallback for people
// without a Wikidata label: a "Mac" name such as "MacTIERNAN" comes out as
// "Mactiernan", since "MACK" can't be told apart.
func casedName(s string) string {
	var b strings.Builder
	start := true
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == ' ' || r == '-' || r == '\'' || r == '’':
			b.WriteRune(r)
			start = true
		case start && strings.HasPrefix(s[i:], "Mc") && len(s) > i+2:
			b.WriteString("Mc")
			n = 2
			start = true
			i += n
			continue
		case start:
			// A particle published in lower case ("van", "de") stays so.
			if !unicode.IsLower(r) {
				r = unicode.ToUpper(r)
			}
			b.WriteRune(r)
			start = false
		default:
			b.WriteRune(unicode.ToLower(r))
		}
		i += n
	}
	return b.String()
}

// personName is the name people know: the Wikidata label when it ends with
// the Handbook family name, otherwise the preferred (or given) name and
// family name from the Handbook.
func personName(in individual, label string) string {
	family := in.DisplayName
	if i := strings.Index(family, ","); i >= 0 {
		family = family[:i]
	}
	family = casedName(strings.TrimSpace(family))
	if family == "" {
		family = casedName(in.FamilyName)
	}
	if label != "" && strings.HasSuffix(strings.ToLower(label), strings.ToLower(family)) {
		return label
	}
	given := in.PreferredName
	if given == "" {
		given = in.GivenName
	}
	return strings.TrimSpace(given + " " + family)
}

func chamber(in individual) string {
	if in.Electorate != "" {
		return "Member"
	}
	for _, c := range in.MPorSenator {
		if c == "Senator" {
			return "Senator"
		}
	}
	if len(in.MPorSenator) > 0 {
		return in.MPorSenator[0]
	}
	return ""
}

// readTitles reads title,portfolio rows. A title with an empty portfolio is
// known to sit in none.
func readTitles(path string) (map[string][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string][]string{}
	for _, r := range rows[1:] {
		ports := out[r[0]]
		if r[1] != "" {
			ports = append(ports, r[1])
		}
		out[r[0]] = ports
	}
	return out, nil
}

func load(ctx context.Context, dbPath, raw, cache string) error {
	var ms struct{ Value []ministry }
	var rs struct{ Value []record }
	var people []individual
	var wd []wikidataPerson
	var photos []photo
	for path, v := range map[string]any{
		ministriesFile: &ms, recordsFile: &rs, individualsFile: &people, wikidataFile: &wd, commonsFile: &photos,
	} {
		if err := readJSON(filepath.Join(raw, path), v); err != nil {
			return err
		}
	}
	titles, err := readTitles(titlesCSV)
	if err != nil {
		return err
	}

	conn, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := db.New(tx)

	const publisher = "Parliamentary Library, Parliament of Australia"
	recordsDoc, err := source.Register(ctx, q, filepath.Join(raw, recordsFile), "Parliamentary Handbook: ministry records",
		publisher, handbookAPI+"ministryrecords", "aph_ministry_records", "")
	if err != nil {
		return err
	}
	peopleDoc, err := source.Register(ctx, q, filepath.Join(raw, individualsFile), "Parliamentary Handbook: parliamentarians",
		publisher, handbookAPI+"individuals", "aph_individuals", "")
	if err != nil {
		return err
	}
	if _, err := source.Register(ctx, q, filepath.Join(raw, wikidataFile), "Wikidata: ministers' Wikipedia articles and images",
		"Wikidata", sparqlURL, "wikidata_ministers", ""); err != nil {
		return err
	}
	if _, err := source.Register(ctx, q, filepath.Join(raw, commonsFile), "Wikimedia Commons: ministers' photos, licences and credits",
		"Wikimedia Commons", commonsAPI, "commons_photos", ""); err != nil {
		return err
	}

	for _, clear := range []func(context.Context) error{q.ClearMinisterHoldings, q.ClearParliamentarians, q.ClearMinistries, q.ClearMinisterTitles} {
		if err := clear(ctx); err != nil {
			return err
		}
	}
	for _, m := range ms.Value {
		if m.MID < firstMinistry {
			continue
		}
		if err := q.InsertMinistry(ctx, db.InsertMinistryParams{ID: int64(m.MID), Name: m.Name, StartDate: m.DateStart, EndDate: m.DateEnd}); err != nil {
			return err
		}
	}

	held := holdings(rs.Value, firstMinistry)
	wdBy := map[string]wikidataPerson{}
	for _, w := range wd {
		wdBy[w.PHID] = w
	}
	photoBy := map[string]photo{}
	for _, p := range photos {
		photoBy[p.File] = p
	}
	personIDs := map[string]int64{}
	var noPhoto []string
	for _, in := range people {
		w := wdBy[in.PHID]
		arg := db.InsertPersonParams{
			AphID:            sql.NullString{String: in.PHID, Valid: true},
			Name:             personName(in, w.Label),
			Party:            in.Party,
			Chamber:          chamber(in),
			Electorate:       in.Electorate,
			State:            in.State,
			ServiceStart:     in.ServiceStart,
			ServiceEnd:       in.ServiceEnd,
			WikidataID:       w.Item,
			WikipediaUrl:     w.Wikipedia,
			SourceDocumentID: peopleDoc,
		}
		if strings.EqualFold(in.InCurrentParliament, "true") {
			arg.InParliament = 1
		}
		if ph, ok := photoBy[w.Image]; ok && w.Image != "" {
			b, err := os.ReadFile(filepath.Join(cache, ph.cacheName()))
			if err == nil {
				arg.Photo, arg.PhotoType = b, ph.Type
				arg.PhotoCredit, arg.PhotoLicence, arg.PhotoLicenceUrl, arg.PhotoPageUrl = ph.Credit, ph.Licence, ph.LicenceURL, ph.Page
			}
		}
		if arg.Photo == nil {
			noPhoto = append(noPhoto, arg.Name)
		}
		id, err := q.InsertPerson(ctx, arg)
		if err != nil {
			return fmt.Errorf("person %s: %w", in.PHID, err)
		}
		personIDs[in.PHID] = id
	}

	unplaced := map[string]bool{}
	written := 0
	for k, h := range held {
		id, ok := personIDs[k.phid]
		if !ok {
			return fmt.Errorf("no Handbook record for %s, who held %q; run with -fetch", k.phid, k.title)
		}
		if err := q.InsertOfficeHolding(ctx, db.InsertOfficeHoldingParams{
			PersonID: id, Role: h.role, Title: k.title, StartDate: k.start, EndDate: h.end, Ministry: h.ministry, SourceDocumentID: recordsDoc,
		}); err != nil {
			return fmt.Errorf("%s %q: %w", k.phid, k.title, err)
		}
		written++
		if _, ok := titles[k.title]; !ok && h.role != "Cabinet Minister" {
			unplaced[k.title] = true
		}
	}

	budget, err := q.LatestBudgetPortfolios(ctx)
	if err != nil {
		return err
	}
	inBudget := map[string]bool{}
	for _, p := range budget {
		inBudget[p] = true
	}
	unknown := map[string]bool{}
	staffed := map[string]bool{}
	for t, ports := range titles {
		for _, p := range ports {
			if !inBudget[p] {
				unknown[p] = true
			}
			if err := q.InsertMinisterTitle(ctx, db.InsertMinisterTitleParams{Title: t, Portfolio: p}); err != nil {
				return err
			}
		}
	}
	for k, h := range held {
		if h.end == "" {
			for _, p := range titles[k.title] {
				staffed[p] = true
			}
		}
	}
	var unstaffed []string
	for _, p := range budget {
		// The Parliament portfolio answers to the Presiding Officers.
		if !staffed[p] && p != "Parliament" {
			unstaffed = append(unstaffed, p)
		}
	}

	var flags []string
	if len(unplaced) > 0 {
		flags = append(flags, fmt.Sprintf("titles not in %s: %s", titlesCSV, strings.Join(sorted(unplaced), "; ")))
	}
	if len(unknown) > 0 {
		flags = append(flags, fmt.Sprintf("portfolios in %s but not the latest Budget: %s", titlesCSV, strings.Join(sorted(unknown), "; ")))
	}
	if len(unstaffed) > 0 {
		flags = append(flags, "portfolios with no current minister: "+strings.Join(unstaffed, "; "))
	}
	sort.Strings(noPhoto)
	notes := fmt.Sprintf("%d people, %d appointments since ministry %d; %d without a photo", len(personIDs), written, firstMinistry, len(noPhoto))
	for _, f := range flags {
		log.Print("FLAG: ", f)
		notes += "; " + f
	}
	if len(noPhoto) > 0 {
		log.Printf("no photo: %s", strings.Join(noPhoto, ", "))
	}
	if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "load-ministers", SourceDocumentID: recordsDoc, RowsRead: int64(len(rs.Value)), RowsWritten: int64(written), Notes: notes,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Print(notes)
	return nil
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
