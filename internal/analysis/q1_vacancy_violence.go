package analysis

import (
	"fmt"
	"sort"

	"github.com/aidan/baltimore-engine/internal/report"
)

// VacancyViolence — Q1: joins DHCD Vacant Building Notices with BPD crime at
// both neighborhood and hundred-block granularity.
type VacancyViolence struct{}

func (VacancyViolence) Number() int { return 1 }
func (VacancyViolence) Title() string {
	return "Vacant Buildings & Violent Crime"
}
func (VacancyViolence) Stakeholder() string {
	return "Mayor's Office & Dept. of Housing and Community Development (DHCD)"
}
func (VacancyViolence) Question() string {
	return "Do neighborhoods and blocks with more open vacant-building notices experience more violent crime, and which neighborhoods should be first in line for coordinated demolition/rehab + public-safety investment?"
}

func (VacancyViolence) Run(in *Input) []report.Element {
	ix := in.IX
	type row struct {
		name                     string
		vac, violent, crimes, sh int
	}
	var rows []row
	var vx, vy []float64
	for _, n := range ix.Neighborhoods {
		if n.Name == "" || (n.Crimes == 0 && n.Vacants == 0) {
			continue
		}
		rows = append(rows, row{n.Name, n.Vacants, n.Violent, n.Crimes, n.Shootings})
	}
	// Fix the order before summing: map iteration is random and float
	// addition is not associative, so this keeps r identical run to run.
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for _, r := range rows {
		vx = append(vx, float64(r.vac))
		vy = append(vy, float64(r.violent))
	}
	rP, rS := pearson(vx, vy), spearman(vx, vy)

	// Group neighborhoods: none vs. quartiles of those with any vacants.
	var nonzero []float64
	for _, r := range rows {
		if r.vac > 0 {
			nonzero = append(nonzero, float64(r.vac))
		}
	}
	q1, q2, q3 := quantile(append([]float64(nil), nonzero...), .25), quantile(append([]float64(nil), nonzero...), .5), quantile(append([]float64(nil), nonzero...), .75)
	labels := []string{
		"No open vacants",
		fmt.Sprintf("Low (1–%.0f)", q1),
		fmt.Sprintf("Medium (%.0f–%.0f)", q1+1, q2),
		fmt.Sprintf("High (%.0f–%.0f)", q2+1, q3),
		fmt.Sprintf("Very high (>%.0f)", q3),
	}
	grpN := make([]int, 5)
	grpViol := make([]float64, 5)
	grpShoot := make([]float64, 5)
	grpVac := make([]int, 5)
	for _, r := range rows {
		g := 0
		v := float64(r.vac)
		switch {
		case r.vac == 0:
			g = 0
		case v <= q1:
			g = 1
		case v <= q2:
			g = 2
		case v <= q3:
			g = 3
		default:
			g = 4
		}
		grpN[g]++
		grpViol[g] += float64(r.violent)
		grpShoot[g] += float64(r.sh)
		grpVac[g] += r.vac
	}
	tbl := report.Table{
		Title:       "Neighborhoods grouped by open vacant-building notices",
		Headers:     []string{"Vacancy tier", "Nbhds", "Vacants", "Avg violent crimes", "Avg shootings"},
		NumericCols: map[int]bool{1: true, 2: true, 3: true, 4: true},
	}
	bars := report.Bars{Title: "Average violent crimes per neighborhood (2025) by vacancy tier", Format: "%.1f"}
	for g := range labels {
		av, as := ratio(grpViol[g], float64(grpN[g])), ratio(grpShoot[g], float64(grpN[g]))
		tbl.Rows = append(tbl.Rows, []string{labels[g], report.Int(grpN[g]), report.Int(grpVac[g]), report.F1(av), report.F1(as)})
		bars.Labels = append(bars.Labels, labels[g])
		bars.Values = append(bars.Values, av)
	}

	// Block-level comparison.
	var vb, ob, vbViol, obViol int
	for _, b := range ix.Blocks {
		if b.Vacants > 0 {
			vb++
			vbViol += b.Violent
		} else {
			ob++
			obViol += b.Violent
		}
	}
	vbRate, obRate := ratio(float64(vbViol), float64(vb)), ratio(float64(obViol), float64(ob))

	// Top 10 neighborhoods by vacants.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].vac != rows[j].vac {
			return rows[i].vac > rows[j].vac
		}
		return rows[i].name < rows[j].name
	})
	top := report.Table{
		Title:       "Top 10 neighborhoods by open vacant-building notices",
		Headers:     []string{"#", "Neighborhood", "Vacants", "Violent", "Shootings", "All crime"},
		NumericCols: map[int]bool{0: true, 2: true, 3: true, 4: true, 5: true},
	}
	topVac, totVac, topViol, totViol := 0, 0, 0, 0
	for i, r := range rows {
		totVac += r.vac
		totViol += r.violent
		if i < 10 {
			topVac += r.vac
			topViol += r.violent
			top.Rows = append(top.Rows, []string{fmt.Sprint(i + 1), report.Name(r.name), report.Int(r.vac), report.Int(r.violent), report.Int(r.sh), report.Int(r.crimes)})
		}
	}

	return []report.Element{
		report.Para{Text: fmt.Sprintf("Join: %s open vacant notices and %s 2025 crime incidents matched on normalised neighborhood name (%d neighborhoods) and on hundred-block address (%s blocks).",
			report.Int(len(in.DS.Vacants)), report.Int(len(in.DS.Crimes)), len(rows), report.Int(len(ix.Blocks)))},
		tbl, bars,
		report.Table{
			Title:       "Hundred-block comparison",
			Headers:     []string{"Block type", "Blocks", "Violent crimes", "Violent per block"},
			NumericCols: map[int]bool{1: true, 2: true, 3: true},
			Rows: [][]string{
				{"Has ≥1 open vacant notice", report.Int(vb), report.Int(vbViol), report.F2(vbRate)},
				{"No open vacant notice", report.Int(ob), report.Int(obViol), report.F2(obRate)},
			},
		},
		top,
		report.Finding{Text: fmt.Sprintf("Correlation between a neighborhood's vacant buildings and its violent crime: Spearman ρ = %.2f, Pearson r = %.2f (%s). Blocks with a vacant building average %.1f× the violent crime of blocks without one.",
			rS, rP, strength(rS), ratio(vbRate, obRate))},
		report.Finding{Text: fmt.Sprintf("The 10 neighborhoods above hold %s of all open vacant notices and %s of violent crime. That makes them the shortlist for pairing DHCD's demolition/stabilisation budget with BPD's place-based violence work.",
			report.Pct(ratio(float64(topVac), float64(totVac))), report.Pct(ratio(float64(topViol), float64(totViol))))},
	}
}
