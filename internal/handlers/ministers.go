package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"spendingexplorer/internal/db"
)

// ministersSection lists who holds each ministerial title in a portfolio, and
// who held those titles before, back to the first ministry loaded.
type ministersSection struct {
	Current       []ministerView
	Earlier       []earlierMinister
	SinceMinistry string // e.g. Morrison Ministry (2nd)
	Since         string // its first day
	Source        db.MinistersSourceRow
}

type ministerView struct {
	ID           int64
	Name         string
	Bio          string
	APHURL       string
	WikipediaURL string
	Photo        bool
	PhotoCredit  string
	PhotoLicence string
	LicenceURL   string
	PhotoPage    string
	Cabinet      bool
	Titles       []heldTitle
	lead         bool
	assistant    bool
	start        string
}

type heldTitle struct {
	Title string
	Start string
}

type earlierMinister struct {
	Name  string
	URL   string
	Title string
	Start string
	End   string
}

// Photos credited by the page, current ministers only.
func (m *ministersSection) Credited() []ministerView {
	var out []ministerView
	for _, c := range m.Current {
		if c.Photo {
			out = append(out, c)
		}
	}
	return out
}

func (h *Handler) portfolioMinisters(ctx context.Context, portfolio string) (*ministersSection, error) {
	rows, err := h.q.PortfolioMinisters(ctx, portfolio)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	s := &ministersSection{}
	if s.Source, err = h.q.MinistersSource(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	first, err := h.q.FirstMinistry(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	s.SinceMinistry, s.Since = first.Name, longDate(first.StartDate)

	byPerson := map[int64]*ministerView{}
	for _, r := range rows {
		if r.EndDate != "" {
			s.Earlier = append(s.Earlier, earlierMinister{
				Name: r.Name, URL: personURL(r.WikipediaUrl, r.AphID), Title: r.Title,
				Start: longDate(r.StartDate), End: longDate(r.EndDate),
			})
			continue
		}
		m, ok := byPerson[r.PersonID]
		if !ok {
			m = &ministerView{
				ID: r.PersonID, Name: r.Name, Bio: bio(r), WikipediaURL: r.WikipediaUrl,
				Photo: r.HasPhoto == 1, PhotoCredit: r.PhotoCredit, PhotoLicence: r.PhotoLicence,
				LicenceURL: r.PhotoLicenceUrl, PhotoPage: r.PhotoPageUrl, Cabinet: r.Cabinet == 1,
				assistant: true, start: r.StartDate,
			}
			if r.AphID != "" {
				m.APHURL = aphURL(r.AphID)
			}
			byPerson[r.PersonID] = m
		}
		m.Titles = append(m.Titles, heldTitle{Title: r.Title, Start: longDate(r.StartDate)})
		m.lead = m.lead || leadTitle(portfolio, r.Title)
		m.assistant = m.assistant && strings.HasPrefix(r.Title, "Assistant ")
		m.start = min(m.start, r.StartDate)
	}
	for _, m := range byPerson {
		// The title that names the portfolio first, as the PBS lists them.
		sort.SliceStable(m.Titles, func(i, j int) bool {
			return leadTitle(portfolio, m.Titles[i].Title) && !leadTitle(portfolio, m.Titles[j].Title)
		})
		s.Current = append(s.Current, *m)
	}
	sort.Slice(s.Current, func(i, j int) bool { return ministerBefore(s.Current[i], s.Current[j]) })
	return s, nil
}

// ministerBefore orders a portfolio's ministers as the PBS does: Cabinet
// ministers first, led by the one whose title names the portfolio, then
// other ministers, then assistant ministers; longest serving first.
func ministerBefore(a, b ministerView) bool {
	switch {
	case a.Cabinet != b.Cabinet:
		return a.Cabinet
	case a.lead != b.lead:
		return a.lead
	case a.assistant != b.assistant:
		return b.assistant
	case a.start != b.start:
		return a.start < b.start
	}
	return a.Name < b.Name
}

// leadTitle reports whether a title names the portfolio it sits in, as
// "Minister for Health and Ageing" names Health, Disability and Ageing.
func leadTitle(portfolio, title string) bool {
	switch title {
	case "Prime Minister":
		return portfolio == "Prime Minister and Cabinet"
	case "Treasurer":
		return portfolio == "Treasury"
	case "Attorney-General":
		return portfolio == "Attorney-General's"
	}
	subject, ok := strings.CutPrefix(title, "Minister for ")
	if !ok {
		return false
	}
	subject = strings.TrimPrefix(subject, "the ")
	return firstWord(subject) == firstWord(portfolio)
}

func firstWord(s string) string {
	w, _, _ := strings.Cut(s, " ")
	return strings.TrimRight(w, ",")
}

// bio is a one-line summary from the Handbook: party, seat and years in
// Parliament.
func bio(r db.PortfolioMinistersRow) string {
	var parts []string
	if r.Party != "" {
		parts = append(parts, r.Party)
	}
	switch {
	case r.InParliament == 0:
		parts = append(parts, "not in Parliament")
	case r.Chamber == "Member" && r.Electorate != "":
		parts = append(parts, "Member for "+r.Electorate+", "+r.State)
	case r.Chamber == "Senator":
		parts = append(parts, "Senator for "+r.State)
	}
	if r.InParliament == 1 && len(r.ServiceStart) >= 4 {
		parts = append(parts, "in Parliament since "+r.ServiceStart[:4])
	}
	return strings.Join(parts, " · ")
}

func aphURL(id string) string {
	return "https://www.aph.gov.au/Senators_and_Members/Parliamentarian?MPID=" + id
}

// personURL links an earlier minister to Wikipedia, or failing that, to the
// Parliament's page.
func personURL(wikipedia, aphID string) string {
	if wikipedia != "" {
		return wikipedia
	}
	if aphID != "" {
		return aphURL(aphID)
	}
	return ""
}

// longDate turns 2025-05-13 into 13 May 2025.
func longDate(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return s
	}
	return t.Format("2 January 2006")
}

func (h *Handler) personPhoto(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := h.q.PersonPhoto(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", p.PhotoType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(p.Photo)
}

// initials stands in for a missing photo: "Sam Rae" gives "SR".
func initials(name string) string {
	var b strings.Builder
	for _, w := range strings.Fields(name) {
		r := []rune(w)
		b.WriteRune(r[0])
	}
	s := []rune(b.String())
	if len(s) > 2 {
		s = []rune{s[0], s[len(s)-1]}
	}
	return string(s)
}
