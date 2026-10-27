// Package corpgroup finds suppliers that belong to one corporate group, such
// as a multinational's Australian subsidiaries or a firm's service companies,
// which AusTender lists under different names and ABNs.
//
// Suppliers are first clustered by the leading word of their names. Sharing a
// word proves nothing (Data#3 and Canberra Data Centres; Boeing and Boeing
// Trailers), so each cluster goes to an LLM, which splits it into groups or
// leaves suppliers alone. This package builds those Batch API requests and
// parses the results. Parent companies reported to GLEIF are joined in
// separately, by cmd/derive.
package corpgroup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	Task          = "supplier-groups"
	PromptVersion = "v2"
	Model         = "claude-haiku-5-5"

	// MaxCluster is the most suppliers a cluster may hold. Larger clusters
	// share a generic word ("Trustee", "National") and are not sent.
	MaxCluster = 100
	// Requests hold whole clusters up to about this many suppliers.
	PerRequest = 300
)

// skipWords are leading words that say nothing about who owns a company.
var skipWords = map[string]bool{"THE": true, "AUSTRALIA": true, "AUSTRALIAN": true, "AUST": true}

// nameEnd cuts a name where a trading name or trust arrangement begins.
var nameEnd = regexp.MustCompile(`\bT/?AS?\b|\bTRADING AS\b|\bATF\b|\bAS TRUSTEE\b|\(|\bFORMERLY\b`)

var word = regexp.MustCompile(`[A-Z0-9&#]+`)

// Key returns the cluster key for a supplier name: its first word that is not
// "The" or "Australia(n)", or "" when there is none of two or more characters.
func Key(name string) string {
	n := strings.ReplaceAll(strings.ToUpper(name), "'", "")
	if loc := nameEnd.FindStringIndex(n); loc != nil {
		n = n[:loc[0]]
	}
	for _, w := range word.FindAllString(n, -1) {
		if skipWords[w] {
			continue
		}
		if len(w) < 2 {
			return ""
		}
		return w
	}
	return ""
}

var actsForTrust = regexp.MustCompile(`(?i)\btrustee\b|\batf\b|\bcustodian\b|\bresponsib(le)? entity\b|\bas trustee\b|\bas nominee\b|\b(a|as) the\b.*\btrust\b`)

// ActsForTrust reports whether a supplier name says the company contracts as
// trustee or custodian for a trust. The trust's property is not the company's,
// so such suppliers are not joined to the trustee company's parent.
func ActsForTrust(name string) bool { return actsForTrust.MatchString(name) }

// Member is one supplier sent for grouping.
type Member struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	EntityType  string `json:"abr_type,omitempty"`
	Country     string `json:"country,omitempty"`
	GLEIFParent string `json:"gleif_parent,omitempty"`
}

// Cluster is suppliers whose names share a key.
type Cluster struct {
	Key     string   `json:"key"`
	Members []Member `json:"suppliers"`
}

// Clusters groups members by Key, keeping clusters of 2 to MaxCluster members,
// sorted by key with members sorted by id.
func Clusters(members []Member) (kept []Cluster, tooLarge []string) {
	by := map[string][]Member{}
	for _, m := range members {
		if k := Key(m.Name); k != "" {
			by[k] = append(by[k], m)
		}
	}
	for k, ms := range by {
		switch {
		case len(ms) < 2:
		case len(ms) > MaxCluster:
			tooLarge = append(tooLarge, k)
		default:
			sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
			kept = append(kept, Cluster{Key: k, Members: ms})
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Key < kept[j].Key })
	sort.Strings(tooLarge)
	return kept, tooLarge
}

// InputHash fingerprints what the model sees for a cluster, so a cluster that
// gains or changes a supplier is sent again.
func InputHash(c Cluster) string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:12])
}

// System is the instruction sent with every request. Changing it requires a
// new PromptVersion so stored results stay comparable.
func System() string {
	return `You group suppliers to the Australian Government into corporate groups. Each cluster lists suppliers, as named in AusTender contract notices, whose names begin with the same word. Where known, each supplier has its Australian Business Register entity type, its country, and the ultimate parent company it reports to GLEIF, the global register of legal entities.

Being wrong is worse than missing a group: a wrong group tells the public that unrelated businesses share an owner. Put suppliers in one group only on one of these two bases:

1. known_group: you can identify the company or group by name from your own knowledge (such as Boeing, KPMG, Accenture, Downer, Serco), and each name is clearly one of its own entities: a subsidiary, division, partnership, service company, regional office or overseas affiliate. The rest of each name must fit that company's business.
2. name_variants: the names are the same organisation's name, differing only in legal form, abbreviation, truncation, state or city, or punctuation (such as "DELL AUSTRALIA PTY LIMITED" and "DELL AUSTRALIA PTY LIM").

Sharing a word is never enough by itself. Do not group:
- organisations you do not recognise whose names share a word but differ otherwise (for example "Karingal St Laurence Limited" and "Karingal Neighbourhood Centre", or "API Services Australia" and "API Services and Solutions");
- names built from a common or descriptive word (Healthcare, Data, Secure, Service, Compass, National, Global), unless basis 2 applies;
- a known brand followed by a business that company is not in (for example "Boeing Trailers" is not part of Boeing);
- joint ventures: a name that combines two companies' names (for example "Serco Sodexo Defence Services" or "Rheinmetall Nioa Munitions") stays out of both companies' groups;
- franchisees and licensees of a brand;
- a company acting as trustee, custodian or responsible entity for a trust;
- government bodies, universities and charities, unless basis 2 applies.

When unsure, leave a supplier out of any group. Name each group as the public knows it, for example "Boeing" or "KPMG". Give the basis, and your confidence: high only when you are certain every member belongs. List only groups of two or more suppliers; leave out suppliers that stand alone. Return every cluster key you are given, with an empty list when no group applies, and use only the supplier ids given in that cluster.`
}

// Prompt is the user message for one request.
func Prompt(clusters []Cluster) (string, error) {
	data, err := json.MarshalIndent(clusters, "", " ")
	if err != nil {
		return "", err
	}
	return "Group the suppliers in these clusters:\n\n" + string(data), nil
}

// Schema is the structured-output JSON schema for a response.
func Schema() map[string]any {
	group := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"name", "basis", "confidence", "members"},
		"properties": map[string]any{
			"name":       map[string]any{"type": "string"},
			"basis":      map[string]any{"type": "string", "enum": []any{"known_group", "name_variants"}},
			"confidence": map[string]any{"type": "string", "enum": []any{"high", "medium", "low"}},
			"members":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
		},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"clusters"},
		"properties": map[string]any{
			"clusters": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []any{"key", "groups"},
					"properties": map[string]any{
						"key":    map[string]any{"type": "string"},
						"groups": map[string]any{"type": "array", "items": group},
					},
				},
			},
		},
	}
}

// Assignment is the model's answer for one supplier in a cluster it returned.
// Label is empty when the supplier stands alone.
type Assignment struct {
	SupplierID int64
	ClusterKey string
	Label      string
	GroupName  string
	Confidence string
}

// Parse reads a response's JSON text. It returns an assignment for every
// supplier in each cluster the response covers; clusters left out of the
// response get none, so they are sent again. A supplier placed in two groups
// keeps the first; groups left with fewer than two suppliers are dropped.
func Parse(text string, sent []Cluster) ([]Assignment, error) {
	var out struct {
		Clusters []struct {
			Key    string `json:"key"`
			Groups []struct {
				Name       string  `json:"name"`
				Confidence string  `json:"confidence"`
				Members    []int64 `json:"members"`
			} `json:"groups"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	byKey := map[string]Cluster{}
	for _, c := range sent {
		byKey[c.Key] = c
	}
	var all []Assignment
	for _, rc := range out.Clusters {
		c, ok := byKey[rc.Key]
		if !ok {
			continue
		}
		delete(byKey, rc.Key) // first answer per cluster wins
		in := map[int64]bool{}
		for _, m := range c.Members {
			in[m.ID] = true
		}
		placed := map[int64]Assignment{}
		n := 0
		for _, g := range rc.Groups {
			var ids []int64
			for _, id := range g.Members {
				if in[id] {
					if _, dup := placed[id]; !dup {
						ids = append(ids, id)
						placed[id] = Assignment{} // reserve
					}
				}
			}
			if len(ids) < 2 {
				for _, id := range ids {
					delete(placed, id)
				}
				continue
			}
			n++
			for _, id := range ids {
				placed[id] = Assignment{Label: fmt.Sprintf("g%d", n), GroupName: g.Name, Confidence: g.Confidence}
			}
		}
		for _, m := range c.Members {
			a := placed[m.ID]
			a.SupplierID, a.ClusterKey = m.ID, c.Key
			all = append(all, a)
		}
	}
	return all, nil
}
