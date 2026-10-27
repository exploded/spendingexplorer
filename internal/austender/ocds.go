// Package austender reads contract notices from the AusTender OCDS API
// (https://api.tenders.gov.au/ocds). Each response is a page of OCDS releases;
// findByDates returns the latest release of each contract, and findById
// returns a contract's full history, including the value before amendments.
package austender

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Australia/Sydney on hosts without zoneinfo

	"spendingexplorer/internal/pbs"
)

// Page is one API response.
type Page struct {
	Releases []Release `json:"releases"`
	Links    struct {
		Next string `json:"next"`
	} `json:"links"`
}

type Release struct {
	OCID    string   `json:"ocid"`
	ID      string   `json:"id"`
	Date    string   `json:"date"`
	Tag     []string `json:"tag"`
	Parties []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Roles       []string `json:"roles"`
		Identifiers []struct {
			ID     string `json:"id"`
			Scheme string `json:"scheme"`
		} `json:"additionalIdentifiers"`
		Address struct {
			Locality   string `json:"locality"`
			Region     string `json:"region"`
			PostalCode string `json:"postalCode"`
			Country    string `json:"countryName"`
		} `json:"address"`
	} `json:"parties"`
	Awards []struct {
		ID        string `json:"id"`
		Suppliers []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"suppliers"`
	} `json:"awards"`
	Contracts []struct {
		ID          string `json:"id"`
		AwardID     string `json:"awardID"`
		DateSigned  string `json:"dateSigned"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Items       []struct {
			Classification struct {
				Scheme string `json:"scheme"`
				ID     string `json:"id"`
			} `json:"classification"`
		} `json:"items"`
		Period struct {
			Start string `json:"startDate"`
			End   string `json:"endDate"`
		} `json:"period"`
		Value struct {
			Currency string `json:"currency"`
			Amount   string `json:"amount"`
		} `json:"value"`
		Status     string `json:"status"`
		Amendments []struct {
			ID              string `json:"id"`
			ReleaseID       string `json:"releaseID"`
			AmendsReleaseID string `json:"amendsReleaseID"`
		} `json:"amendments"`
	} `json:"contracts"`
	Tender struct {
		ProcurementMethod   string `json:"procurementMethod"`
		MethodDetails       string `json:"procurementMethodDetails"`
		LimitedTenderReason string `json:"limitedTenderReason"`
		LimitedTenderExempt string `json:"limitedTenderExempt"`
		Exemption           string `json:"exemption"`
		ExemptionCode       string `json:"exemptionCode"`
	} `json:"tender"`
}

// Contract is the flattened state of a contract notice in one release.
type Contract struct {
	CNID             string
	OCID             string
	ReleaseID        string
	ReleaseDate      string // YYYY-MM-DD, Australian Eastern time
	Amendment        bool
	AmendmentIDs     []string
	Agency           string
	AgencyABN        string
	Supplier         string
	SupplierABN      string
	SupplierCountry  string
	SupplierPostcode string
	Title            string
	Description      string
	UNSPSC           string
	Method           string // open | limited | selective
	MethodDetail     string
	LimitedReason    string
	Exemption        string
	StartDate        string
	EndDate          string
	Signed           string
	Value            int64 // whole dollars, AUD
	Currency         string
	Status           string
}

var sydney, _ = time.LoadLocation("Australia/Sydney")

// LocalDate converts an API timestamp (UTC, usually local midnight) to a
// YYYY-MM-DD date in Australian Eastern time.
func LocalDate(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	return t.In(sydney).Format("2006-01-02")
}

// Flatten flattens a release. A release normally holds one contract.
func (r Release) Flatten() ([]Contract, error) {
	var agency, agencyABN string
	type party struct{ name, abn, country, postcode string }
	parties := map[string]party{}
	for _, p := range r.Parties {
		var abn string
		for _, id := range p.Identifiers {
			if id.Scheme == "AU-ABN" {
				abn = normaliseABN(id.ID)
			}
		}
		parties[p.ID] = party{pbs.CleanText(p.Name), abn, p.Address.Country, p.Address.PostalCode}
		for _, role := range p.Roles {
			if role == "procuringEntity" {
				agency, agencyABN = pbs.CleanText(p.Name), abn
			}
		}
	}
	isAmendment := false
	for _, t := range r.Tag {
		if t == "contractAmendment" {
			isAmendment = true
		}
	}

	var out []Contract
	for _, c := range r.Contracts {
		value, err := parseValue(c.Value.Amount)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.ID, err)
		}
		ct := Contract{
			CNID:          c.ID,
			OCID:          r.OCID,
			ReleaseID:     r.ID,
			ReleaseDate:   LocalDate(r.Date),
			Amendment:     isAmendment,
			Agency:        agency,
			AgencyABN:     agencyABN,
			Title:         pbs.CleanText(c.Title),
			Description:   pbs.CleanText(c.Description),
			Method:        r.Tender.ProcurementMethod,
			MethodDetail:  r.Tender.MethodDetails,
			LimitedReason: pbs.CleanText(r.Tender.LimitedTenderReason),
			Exemption:     pbs.CleanText(r.Tender.Exemption),
			StartDate:     LocalDate(c.Period.Start),
			EndDate:       LocalDate(c.Period.End),
			Signed:        LocalDate(c.DateSigned),
			Value:         value,
			Currency:      c.Value.Currency,
			Status:        c.Status,
		}
		for _, a := range c.Amendments {
			ct.AmendmentIDs = append(ct.AmendmentIDs, a.ID)
		}
		for _, it := range c.Items {
			if it.Classification.Scheme == "UNSPSC" {
				ct.UNSPSC = it.Classification.ID
				break
			}
		}
		for _, a := range r.Awards {
			if a.ID != c.AwardID {
				continue
			}
			for _, s := range a.Suppliers {
				p := parties[s.ID]
				ct.Supplier, ct.SupplierABN, ct.SupplierCountry, ct.SupplierPostcode = p.name, p.abn, p.country, p.postcode
				if ct.Supplier == "" {
					ct.Supplier = pbs.CleanText(s.Name)
				}
				break
			}
		}
		out = append(out, ct)
	}
	return out, nil
}

func parseValue(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	if err != nil {
		return 0, fmt.Errorf("bad value %q", s)
	}
	return int64(math.Round(f)), nil
}

// normaliseABN keeps digits only; anything that is not 11 digits is dropped.
func normaliseABN(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() != 11 {
		return ""
	}
	return b.String()
}

// ParsePage decodes an API response.
func ParsePage(data []byte) (*Page, error) {
	var p Page
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
