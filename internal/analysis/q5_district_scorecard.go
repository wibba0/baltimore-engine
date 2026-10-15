package analysis

import (
	"fmt"
	"sort"

	"github.com/aidan/baltimore-engine/internal/report"
)

// DistrictScorecard — Q5: rolls all four datasets up to the 14 City Council
// districts. Crime records carry no council district, and liquor licenses
// carry no neighborhood, so this is a two-hop join:
//
//	liquor ─(block key)→ block ─(majority vote)→ neighborhood
//	crime  ─────────────────────────────────────→ neighborhood
//	neighborhood ─(majority vote of 311 + vacant records)→ council district
type DistrictScorecard struct{}

func (DistrictScorecard) Number() int   { return 5 }
func (DistrictScorecard) Title() string { return "City Council District Priority Scorecard" }
func (DistrictScorecard) Stakeholder() string {
	return "City Council & Dept. of Planning (budget / CIP prioritisation)"
}
func (DistrictScorecard) Question() string {
	return "Combining violence, vacancy, blight demand, liquor density and 311 service performance, which council districts carry the heaviest combined burden and should be first in line for the next capital-budget cycle?"
}

type district struct {
	n                                      uint8
	nbhds, violent, shootings, vacants     int
	blight, liquor, req, pastDue, dueKnown int
	days                                   []float32
}

func (DistrictScorecard) Run(in *Input) []report.Element {
	ds := make([]district, 15)
	unassigned := 0
	for _, n := range in.IX.Neighborhoods {
		c := n.Council()
		if c == 0 {
			unassigned++
			continue
		}
		d := &ds[c]
		d.n = c
		d.nbhds++
		d.violent += n.Violent
		d.shootings += n.Shootings
		d.vacants += n.Vacants
		d.blight += n.Blight
		d.liquor += n.Liquor
		d.req += n.FieldRequests
		d.pastDue += n.PastDue
		d.dueKnown += n.DueKnown
		d.days = append(d.days, n.CloseDays...)
	}
	var list []district
	for _, d := range ds[1:] {
		if d.n != 0 {
			list = append(list, d)
		}
	}

	metrics := []struct {
		name string
		get  func(d *district) float64
	}{
		{"violent crime", func(d *district) float64 { return float64(d.violent) }},
		{"shootings", func(d *district) float64 { return float64(d.shootings) }},
		{"vacant buildings", func(d *district) float64 { return float64(d.vacants) }},
		{"blight requests", func(d *district) float64 { return float64(d.blight) }},
		{"liquor outlets", func(d *district) float64 { return float64(d.liquor) }},
		{"311 overdue rate", func(d *district) float64 { return ratio(float64(d.pastDue), float64(d.dueKnown)) }},
	}
	norm := make([][]float64, len(metrics))
	for m, mt := range metrics {
		raw := make([]float64, len(list))
		for i := range list {
			raw[i] = mt.get(&list[i])
		}
		norm[m] = minMax(raw)
	}
	score := make([]float64, len(list))
	driver := make([]string, len(list))
	for i := range list {
		best := -1.0
		for m := range metrics {
			score[i] += norm[m][i]
			if norm[m][i] > best {
				best, driver[i] = norm[m][i], metrics[m].name
			}
		}
		score[i] = score[i] / float64(len(metrics)) * 100
	}
	idx := make([]int, len(list))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return score[idx[a]] > score[idx[b]] }) // ties: lower district number first

	tbl := report.Table{
		Title:       "Council district scorecard (2025), ranked by composite burden score",
		Headers:     []string{"Rank", "District", "Nbhds", "Violent", "Shootings", "Vacants", "Blight 311", "Liquor", "Med. days", "Overdue", "Score"},
		NumericCols: map[int]bool{0: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true, 10: true},
	}
	bars := report.Bars{Title: "Composite burden score (0 = lowest district on every metric, 100 = highest on every metric)", Format: "%.0f"}
	for r, i := range idx {
		d := &list[i]
		tbl.Rows = append(tbl.Rows, []string{fmt.Sprint(r + 1), fmt.Sprintf("District %d", d.n), report.Int(d.nbhds),
			report.Int(d.violent), report.Int(d.shootings), report.Int(d.vacants), report.Int(d.blight), report.Int(d.liquor),
			report.F2(quantile32(d.days, .5)), report.Pct(ratio(float64(d.pastDue), float64(d.dueKnown))), report.F1(score[i])})
		bars.Labels = append(bars.Labels, fmt.Sprintf("District %d", d.n))
		bars.Values = append(bars.Values, score[i])
	}

	var top []string
	for r := 0; r < min(3, len(idx)); r++ {
		i := idx[r]
		top = append(top, fmt.Sprintf("District %d (score %.0f, driven most by %s)", list[i].n, score[i], driver[i]))
	}
	return []report.Element{
		report.Para{Text: fmt.Sprintf("Each neighborhood is assigned to the council district most of its 311 and vacant-notice records report (%d neighborhoods could not be assigned). Six metrics are min-max scaled across the %d districts and averaged with equal weight into a 0–100 burden score.",
			unassigned, len(list))},
		tbl, bars,
		report.Finding{Text: "Highest combined burden: " + join(top) + "."},
		report.Finding{Text: "Note: the score uses raw counts, not rates, because Open Baltimore does not publish population by district. It ranks where the volume of need is, which is what capital budgeting needs. It is not a per-resident risk measure."},
	}
}

func join(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	out := ""
	for i, x := range xs {
		switch {
		case i == 0:
			out = x
		case i == len(xs)-1:
			out += "; and " + x
		default:
			out += "; " + x
		}
	}
	return out
}
