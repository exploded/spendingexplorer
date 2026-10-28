package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	handbookAPI = "https://handbookapi.aph.gov.au/api/"
	sparqlURL   = "https://query.wikidata.org/sparql"
	commonsAPI  = "https://commons.wikimedia.org/w/api.php"
	userAgent   = "spendingexplorer (+https://github.com/exploded/spendingexplorer)"
	photoWidth  = 240
)

var client = &http.Client{Timeout: 120 * time.Second}

func get(ctx context.Context, u string, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// fetch refreshes the Handbook, Wikidata and Commons snapshots in data/raw,
// and downloads any photo not already in the cache.
func fetch(ctx context.Context, raw, cache string, first int) error {
	ministries, err := get(ctx, handbookAPI+"ministries", "application/json")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(raw, ministriesFile), ministries, 0o644); err != nil {
		return err
	}
	// Every record is kept, so a longer history needs no new download.
	records, err := get(ctx, handbookAPI+"ministryrecords", "application/json")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(raw, recordsFile), records, 0o644); err != nil {
		return err
	}
	var rs struct{ Value []record }
	if err := json.Unmarshal(records, &rs); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, r := range rs.Value {
		if r.MID >= first {
			ids[r.PHID] = true
		}
	}
	phids := make([]string, 0, len(ids))
	for id := range ids {
		phids = append(phids, id)
	}
	sort.Strings(phids)
	log.Printf("%d people in ministries from number %d", len(phids), first)

	people, err := fetchIndividuals(ctx, phids)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(raw, individualsFile), people); err != nil {
		return err
	}

	wd, err := fetchWikidata(ctx, phids)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(raw, wikidataFile), wd); err != nil {
		return err
	}

	photos, err := fetchCommons(ctx, wd, cache)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(raw, commonsFile), photos)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// individualFields are the parts of a Handbook record the site uses.
const individualFields = "PHID,GivenName,PreferredName,FamilyName,DisplayName,Party,Electorate,State,MPorSenator,InCurrentParliament,ServiceHistory_Start,ServiceHistory_End"

func fetchIndividuals(ctx context.Context, phids []string) ([]individual, error) {
	var out []individual
	// The API refuses filters of more than about 16 terms.
	for i := 0; i < len(phids); i += 10 {
		chunk := phids[i:min(i+10, len(phids))]
		var terms []string
		for _, id := range chunk {
			terms = append(terms, "PHID eq '"+id+"'")
		}
		q := url.Values{"$filter": {strings.Join(terms, " or ")}, "$select": {individualFields}}
		b, err := get(ctx, handbookAPI+"individuals?"+q.Encode(), "application/json")
		if err != nil {
			return nil, err
		}
		var page struct{ Value []individual }
		if err := json.Unmarshal(b, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Value...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PHID < out[j].PHID })
	return out, nil
}

// fetchWikidata finds each person's Wikidata item by Handbook ID (P10020),
// with its English label, image (P18) and English Wikipedia article.
func fetchWikidata(ctx context.Context, phids []string) ([]wikidataPerson, error) {
	var values []string
	for _, id := range phids {
		values = append(values, `"`+id+`"`)
	}
	query := `SELECT ?phid ?item ?label ?image ?article WHERE {
  VALUES ?phid { ` + strings.Join(values, " ") + ` }
  ?item wdt:P10020 ?phid .
  OPTIONAL { ?item rdfs:label ?label FILTER(LANG(?label) = "en") }
  OPTIONAL { ?item wdt:P18 ?image }
  OPTIONAL { ?article schema:about ?item ; schema:isPartOf <https://en.wikipedia.org/> }
}`
	b, err := get(ctx, sparqlURL+"?"+url.Values{"query": {query}}.Encode(), "application/sparql-results+json")
	if err != nil {
		return nil, err
	}
	type val struct{ Value string }
	var res struct {
		Results struct {
			Bindings []map[string]val
		}
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	// A person can have several images; keep the first by name, so a refresh
	// doesn't swap photos at random.
	byID := map[string]*wikidataPerson{}
	for _, row := range res.Results.Bindings {
		id := row["phid"].Value
		p, ok := byID[id]
		if !ok {
			p = &wikidataPerson{PHID: id, Item: strings.TrimPrefix(row["item"].Value, "http://www.wikidata.org/entity/")}
			byID[id] = p
		}
		if l := row["label"].Value; l != "" && p.Label == "" {
			p.Label = l
		}
		if a := row["article"].Value; a != "" && p.Wikipedia == "" {
			p.Wikipedia = a
		}
		if f := commonsFileName(row["image"].Value); f != "" && (p.Image == "" || f < p.Image) {
			p.Image = f
		}
	}
	out := make([]wikidataPerson, 0, len(byID))
	for _, p := range byID {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PHID < out[j].PHID })
	return out, nil
}

// commonsFileName turns a Special:FilePath URL into a Commons file name.
func commonsFileName(u string) string {
	i := strings.LastIndex(u, "/Special:FilePath/")
	if i < 0 {
		return ""
	}
	name, err := url.PathUnescape(u[i+len("/Special:FilePath/"):])
	if err != nil {
		return ""
	}
	return name
}

var tags = regexp.MustCompile(`<[^>]*>`)

// plain strips the HTML Commons puts in its credit fields.
func plain(s string) string {
	s = tags.ReplaceAllString(s, "")
	s = strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// fetchCommons looks up each photo's licence and credit, and downloads a
// thumbnail into the cache when it isn't there already.
func fetchCommons(ctx context.Context, people []wikidataPerson, cache string) ([]photo, error) {
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return nil, err
	}
	var files []string
	for _, p := range people {
		if p.Image != "" {
			files = append(files, p.Image)
		}
	}
	sort.Strings(files)
	var out []photo
	for i := 0; i < len(files); i += 40 {
		chunk := files[i:min(i+40, len(files))]
		titles := make([]string, len(chunk))
		for j, f := range chunk {
			titles[j] = "File:" + f
		}
		q := url.Values{
			"action": {"query"}, "format": {"json"}, "formatversion": {"2"}, "prop": {"imageinfo"},
			"iiprop": {"url|extmetadata|mime"}, "iiurlwidth": {fmt.Sprint(photoWidth)},
			"iiextmetadatafilter": {"LicenseShortName|LicenseUrl|Artist|Credit"},
			"titles":              {strings.Join(titles, "|")},
		}
		b, err := get(ctx, commonsAPI+"?"+q.Encode(), "application/json")
		if err != nil {
			return nil, err
		}
		type field struct{ Value string }
		var res struct {
			Query struct {
				Pages []struct {
					Title     string
					Missing   bool
					ImageInfo []struct {
						ThumbURL       string `json:"thumburl"`
						ThumbMime      string `json:"thumbmime"`
						Mime           string
						DescriptionURL string `json:"descriptionurl"`
						ExtMetadata    map[string]field
					}
				}
			}
		}
		if err := json.Unmarshal(b, &res); err != nil {
			return nil, err
		}
		for _, pg := range res.Query.Pages {
			if pg.Missing || len(pg.ImageInfo) == 0 {
				log.Printf("no Commons file %q", pg.Title)
				continue
			}
			ii := pg.ImageInfo[0]
			m := ii.ExtMetadata
			ph := photo{
				File:       strings.TrimPrefix(pg.Title, "File:"),
				ThumbURL:   ii.ThumbURL,
				Type:       ii.ThumbMime,
				Page:       ii.DescriptionURL,
				Licence:    plain(m["LicenseShortName"].Value),
				LicenceURL: m["LicenseUrl"].Value,
				Credit:     plain(m["Artist"].Value),
			}
			if ph.Type == "" {
				ph.Type = ii.Mime
			}
			if ph.Credit == "" {
				ph.Credit = plain(m["Credit"].Value)
			}
			if err := download(ctx, ph, cache); err != nil {
				log.Printf("photo %q: %v", ph.File, err)
				continue
			}
			out = append(out, ph)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	log.Printf("%d photos of %d", len(out), len(files))
	return out, nil
}

func download(ctx context.Context, ph photo, cache string) error {
	path := filepath.Join(cache, ph.cacheName())
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	b, err := get(ctx, ph.ThumbURL, "")
	if err != nil {
		return err
	}
	// Wikimedia asks bulk clients to go gently.
	time.Sleep(250 * time.Millisecond)
	return os.WriteFile(path, b, 0o644)
}
