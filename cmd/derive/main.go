// Command derive rebuilds derived tables from loaded data: each contract's
// value spread across financial years (contract_fy), suppliers who may be
// individuals (supplier_individual), corporate groups of suppliers
// (supplier_group), grant recipients matched to suppliers by name
// (grant_recipient_supplier), corporate entities from the reviewed list
// (corporate_agency), and the "worth a closer look" signals (signal). Run it
// after any loader.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"spendingexplorer/internal/austender"
	"spendingexplorer/internal/corpgroup"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/fiscal"
	"spendingexplorer/internal/grantconnect"
)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	corporate := flag.String("corporate", "data/corporate_agencies.csv", "reviewed AusTender agencies that are corporate Commonwealth entities")
	flag.Parse()

	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer tx.Rollback()
	q := db.New(tx)

	contracts, err := q.ContractsForApportion(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if err := q.ClearContractFY(ctx); err != nil {
		log.Fatal(err)
	}
	rows, skipped := 0, 0
	for _, c := range contracts {
		start := c.StartDate
		if start == "" {
			start = c.PublishedDate
		}
		shares, err := fiscal.Apportion(c.Value, start, c.EndDate)
		if err != nil {
			skipped++
			continue
		}
		for _, s := range shares {
			if err := q.InsertContractFY(ctx, db.InsertContractFYParams{CnID: c.CnID, FiscalYear: s.FiscalYear, Amount: s.Amount}); err != nil {
				log.Fatal(err)
			}
			rows++
		}
	}
	fmt.Printf("contract_fy: %d contracts, %d year rows, %d skipped (no usable dates)\n", len(contracts), rows, skipped)

	// Suppliers who are, or may be, individuals: registered as individuals in
	// the ABR, or with no ABR record and a name that reads as a person's.
	if err := q.ClearSupplierIndividuals(ctx); err != nil {
		log.Fatal(err)
	}
	if err := q.DeriveABRIndividuals(ctx); err != nil {
		log.Fatal(err)
	}
	names, err := q.ListGivenNames(ctx)
	if err != nil {
		log.Fatal(err)
	}
	given := make(map[string]bool, len(names))
	for _, n := range names {
		given[n] = true
	}
	unregistered, err := q.SuppliersWithoutABR(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range unregistered {
		if austender.LooksLikePerson(s.Name, given) {
			if err := q.InsertSupplierIndividual(ctx, db.InsertSupplierIndividualParams{SupplierID: s.ID, Basis: "name"}); err != nil {
				log.Fatal(err)
			}
		}
	}
	inds, err := q.SupplierIndividualCounts(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, c := range inds {
		fmt.Printf("individual suppliers (%s): %d\n", c.Basis, c.Suppliers)
	}

	if err := deriveGroups(ctx, q); err != nil {
		log.Fatal(err)
	}
	if err := deriveGrantSuppliers(ctx, q); err != nil {
		log.Fatal(err)
	}

	// Corporate entities have a higher open-tender threshold; the contract_pub
	// view reads this table, so it must be loaded before the signals.
	if err := loadCorporate(ctx, q, *corporate); err != nil {
		log.Fatal(err)
	}

	// Signals: each rule in internal/signals has one derivation query, except
	// late-batch, which takes batched matches from late-publication.
	if err := q.ClearSignals(ctx); err != nil {
		log.Fatal(err)
	}
	for _, derive := range []func(context.Context) error{
		q.DeriveValueIncrease, q.DeriveLatePublication, q.DeriveLateBatch, q.RemoveBatchedLate,
		q.DeriveThresholdCluster, q.DeriveRepeatLimited, q.DeriveCrossedThreshold, q.DeriveNewABN,
	} {
		if err := derive(ctx); err != nil {
			log.Fatal(err)
		}
	}
	counts, err := q.SignalCounts(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, c := range counts {
		fmt.Printf("signal %-18s %7d contracts  $%.1fm\n", c.RuleID, c.Contracts, float64(c.Amount)/1e6)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
}

// deriveGrantSuppliers matches grant recipients to suppliers by name key,
// since GrantConnect publishes no ABN. A key shared by more than one supplier
// is ambiguous and left unmatched, as is a supplier who may be an individual.
func deriveGrantSuppliers(ctx context.Context, q *db.Queries) error {
	if err := q.ClearGrantRecipientSuppliers(ctx); err != nil {
		return err
	}
	sups, err := q.SupplierNames(ctx)
	if err != nil {
		return err
	}
	type match struct {
		id         int64
		n          int
		individual bool
	}
	byKey := map[string]*match{}
	for _, s := range sups {
		k := grantconnect.RecipientKey(s.Name)
		if k == "" {
			continue
		}
		if m := byKey[k]; m != nil {
			m.n++
			continue
		}
		byKey[k] = &match{id: s.ID, n: 1, individual: s.Individual == 1}
	}
	keys, err := q.GrantRecipientKeys(ctx)
	if err != nil {
		return err
	}
	matched := 0
	for _, k := range keys {
		m := byKey[k]
		if m == nil || m.n > 1 || m.individual {
			continue
		}
		if err := q.InsertGrantRecipientSupplier(ctx, db.InsertGrantRecipientSupplierParams{RecipientKey: k, SupplierID: m.id}); err != nil {
			return err
		}
		matched++
	}
	fmt.Printf("grant recipients matched to suppliers by name: %d of %d\n", matched, len(keys))
	return nil
}

// unionFind joins string nodes into components.
type unionFind map[string]string

func (u unionFind) find(x string) string {
	if _, ok := u[x]; !ok {
		u[x] = x
	}
	for u[x] != x {
		u[x] = u[u[x]]
		x = u[x]
	}
	return x
}

func (u unionFind) union(a, b string) { u[u.find(a)] = u.find(b) }

func supNode(id int64) string { return fmt.Sprintf("s%d", id) }

// deriveGroups rebuilds supplier_group: suppliers joined because they report
// the same ultimate parent to GLEIF (or one is the others' parent), or because
// the LLM placed them in one group. Suppliers contracting as trustee for a
// trust are not joined through GLEIF.
func deriveGroups(ctx context.Context, q *db.Queries) error {
	links, err := q.GLEIFSupplierLinks(ctx)
	if err != nil {
		return err
	}
	ai, err := q.AISupplierGroups(ctx, corpgroup.PromptVersion)
	if err != nil {
		return err
	}

	all, gleif := unionFind{}, unionFind{}
	isSupplier := map[string]int64{}
	parentName := map[string]string{} // LEI -> legal name, for parents
	familyOf := map[int64]string{}    // supplier -> the parent LEI it reports, or its own LEI
	isParent := map[string]bool{}
	for _, l := range links {
		if l.ParentLei != "" {
			isParent[l.ParentLei] = true
			parentName[l.ParentLei] = l.ParentName
		}
	}
	for _, l := range links {
		if corpgroup.ActsForTrust(l.Name) {
			continue
		}
		s := supNode(l.ID)
		isSupplier[s] = l.ID
		for _, u := range []unionFind{all, gleif} {
			u.union(s, "lei:"+l.Lei)
			if l.ParentLei != "" {
				u.union("lei:"+l.Lei, "lei:"+l.ParentLei)
			}
		}
		switch {
		case l.ParentLei != "":
			familyOf[l.ID] = l.ParentLei
		case isParent[l.Lei]:
			familyOf[l.ID] = l.Lei
			parentName[l.Lei] = l.LegalName
		}
	}
	aiName := map[int64]string{}
	for _, a := range ai {
		// A supplier renamed since it was sent no longer belongs to the cluster.
		if corpgroup.Key(a.Name) != a.ClusterKey || corpgroup.ActsForTrust(a.Name) {
			continue
		}
		s := supNode(a.SupplierID)
		isSupplier[s] = a.SupplierID
		all.union(s, "ai:"+a.ClusterKey+"/"+a.GroupLabel)
		aiName[a.SupplierID] = a.GroupName
	}

	// Suppliers per component, and per GLEIF-only component.
	members := map[string][]int64{}
	gleifSize := map[string]int{}
	for s, id := range isSupplier {
		members[all.find(s)] = append(members[all.find(s)], id)
		if _, ok := gleif[s]; ok {
			gleifSize[gleif.find(s)]++
		}
	}
	if err := q.ClearSupplierGroupMembers(ctx); err != nil {
		return err
	}
	if err := q.ClearSupplierGroups(ctx); err != nil {
		return err
	}
	groups, joined, byGLEIF := 0, 0, 0
	for _, ids := range members {
		if len(ids) < 2 {
			continue
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		// Name the group as the model did (most common name among members),
		// else by the parent most members report.
		names, parents := map[string]int{}, map[string]int{}
		for _, id := range ids {
			if n := aiName[id]; n != "" {
				names[n]++
			}
			if p := familyOf[id]; p != "" {
				parents[p]++
			}
		}
		parent, name, basis := mostCommon(parents), mostCommon(names), "ai"
		if name == "" {
			name, basis = parentName[parent], "gleif"
		}
		if name == "" {
			continue // cannot happen: every group has an AI name or a GLEIF parent
		}
		if err := q.InsertSupplierGroup(ctx, db.InsertSupplierGroupParams{ID: ids[0], Name: name, NameBasis: basis, ParentLei: parent}); err != nil {
			return err
		}
		for _, id := range ids {
			b := "ai"
			if s := supNode(id); gleif[s] != "" && gleifSize[gleif.find(s)] >= 2 {
				b = "gleif"
				byGLEIF++
			}
			if err := q.InsertSupplierGroupMember(ctx, db.InsertSupplierGroupMemberParams{SupplierID: id, GroupID: ids[0], Basis: b}); err != nil {
				return err
			}
		}
		groups++
		joined += len(ids)
	}
	fmt.Printf("supplier groups: %d, joining %d suppliers (%d through a GLEIF parent)\n", groups, joined, byGLEIF)
	return nil
}

// mostCommon returns the key with the highest count, ties to the smallest key.
func mostCommon(counts map[string]int) string {
	best, n := "", 0
	for k, c := range counts {
		if c > n || (c == n && k < best) {
			best, n = k, c
		}
	}
	return best
}

// loadCorporate replaces corporate_agency with the agencies in the reviewed
// CSV (columns: agency, flipchart_name). Names with no loaded agency are
// reported, since the list should track AusTender's agency names.
func loadCorporate(ctx context.Context, q *db.Queries, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := q.ClearCorporateAgencies(ctx); err != nil {
		return err
	}
	matched := 0
	for _, rec := range records[1:] {
		n, err := q.InsertCorporateAgency(ctx, rec[0])
		if err != nil {
			return err
		}
		if n == 0 {
			fmt.Printf("corporate agency not loaded: %s\n", rec[0])
		}
		matched += int(n)
	}
	fmt.Printf("corporate agencies: %d of %d\n", matched, len(records)-1)
	return nil
}
