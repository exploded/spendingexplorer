package handlers

import (
	"context"

	"spendingexplorer/internal/db"
)

// reportedSection is the contract spending an entity reported in its annual
// reports (Transparency Portal). Figures are shown as published; those that
// fail a consistency check carry a numbered note.
type reportedSection struct {
	Years          []reportedYear
	OrgYear        string
	Consultancy    []reportedOrg
	NonConsultancy []reportedOrg
	Notes          []string
}

type reportedYear struct {
	Year           string
	Consultancy    *reportedFigure
	NonConsultancy *reportedFigure
}

type reportedFigure struct {
	Amount, Contracts int64
	Note              int // 1-based index into Notes, or 0
}

type reportedOrg struct {
	db.EntityReportedOrgsRow
	Note int
}

func (h *Handler) entityReported(ctx context.Context, entityID int64) (*reportedSection, error) {
	rows, err := h.q.EntityReportedSpend(ctx, sqlNullInt(entityID))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	sec := &reportedSection{OrgYear: rows[0].ReportingYear}
	note := func(text string) int {
		if text == "" {
			return 0
		}
		for i, n := range sec.Notes {
			if n == text {
				return i + 1
			}
		}
		sec.Notes = append(sec.Notes, text)
		return len(sec.Notes)
	}
	for _, r := range rows {
		if len(sec.Years) == 0 || sec.Years[len(sec.Years)-1].Year != r.ReportingYear {
			sec.Years = append(sec.Years, reportedYear{Year: r.ReportingYear})
		}
		y := &sec.Years[len(sec.Years)-1]
		f := &reportedFigure{Amount: r.Amount, Contracts: r.Contracts, Note: note(r.CheckNote)}
		if r.Kind == "consultancy" {
			y.Consultancy = f
		} else {
			y.NonConsultancy = f
		}
	}
	orgs, err := h.q.EntityReportedOrgs(ctx, db.EntityReportedOrgsParams{EntityID: sqlNullInt(entityID), ReportingYear: sec.OrgYear})
	if err != nil {
		return nil, err
	}
	for _, o := range orgs {
		ro := reportedOrg{EntityReportedOrgsRow: o, Note: note(o.CheckNote)}
		if o.Kind == "consultancy" {
			sec.Consultancy = append(sec.Consultancy, ro)
		} else {
			sec.NonConsultancy = append(sec.NonConsultancy, ro)
		}
	}
	return sec, nil
}
