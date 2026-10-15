package analysis

import (
	"fmt"
	"math"
	"sort"

	"github.com/aidan/baltimore-engine/internal/index"
	"github.com/aidan/baltimore-engine/internal/report"
)

// BlightVacancy — Q4: joins resident-reported sanitation/nuisance 311
// requests with vacant-building notices at hundred-block level.
type BlightVacancy struct{}

func (BlightVacancy) Number() int   { return 4 }
func (BlightVacancy) Title() string { return "Vacancy-Driven Sanitation & Blight Demand" }
func (BlightVacancy) Stakeholder() string {
	return "Dept. of Public Works (Solid Waste) & DHCD Code Enforcement"
}
func (BlightVacancy) Question() string {
	return "How much resident-reported blight (illegal dumping, dirty alleys, rats, open/unsecured properties) is generated on blocks that contain vacant buildings? Does the demand rise with the number of vacants on a block?"
}

func (BlightVacancy) Run(in *Input) []report.Element {
	ix, ds := in.IX, in.DS

	tiers := []struct {
		label  string
		lo, hi int
	}{{"0 vacants", 0, 0}, {"1 vacant", 1, 1}, {"2–4 vacants", 2, 4}, {"5–9 vacants", 5, 9}, {"10+ vacants", 10, 1 << 30}}
	blocks := make([]int, len(tiers))
	blight := make([]int, len(tiers))
	anyB := make([]int, len(tiers))
	tierOf := func(v int) int {
		for i, t := range tiers {
			if v >= t.lo && v <= t.hi {
				return i
			}
		}
		return 0
	}
	totalBlight, vacBlight, vacBlocks := 0, 0, 0
	for _, b := range ix.Blocks {
		t := tierOf(b.Vacants)
		blocks[t]++
		blight[t] += b.Blight
		if b.Blight > 0 {
			anyB[t]++
		}
		totalBlight += b.Blight
		if b.Vacants > 0 {
			vacBlight += b.Blight
			vacBlocks++
		}
	}
	tbl := report.Table{
		Title:       "Hundred-blocks by number of open vacant-building notices",
		Headers:     []string{"Vacants on block", "Blocks", "Blight requests", "Per block", "Blocks w/ any blight"},
		NumericCols: map[int]bool{1: true, 2: true, 3: true, 4: true},
	}
	bars := report.Bars{Title: "Resident-reported blight requests per block (dose-response)", Format: "%.2f"}
	rates := make([]float64, len(tiers))
	for i, t := range tiers {
		rates[i] = ratio(float64(blight[i]), float64(blocks[i]))
		tbl.Rows = append(tbl.Rows, []string{t.label, report.Int(blocks[i]), report.Int(blight[i]), report.F2(rates[i]),
			report.Pct(ratio(float64(anyB[i]), float64(blocks[i])))})
		bars.Labels = append(bars.Labels, t.label)
		bars.Values = append(bars.Values, rates[i])
	}

	// Per-type rates on vacant vs non-vacant blocks.
	type tcount struct{ vac, other int }
	byType := map[string]*tcount{}
	for i := range ds.Requests {
		r := &ds.Requests[i]
		if r.Block == "" || !index.IsBlight(r.Type) {
			continue
		}
		b := ix.Blocks[r.Block]
		tc := byType[r.Type]
		if tc == nil {
			tc = &tcount{}
			byType[r.Type] = tc
		}
		if b.Vacants > 0 {
			tc.vac++
		} else {
			tc.other++
		}
	}
	type trow struct {
		name         string
		vac, other   int
		rv, ro, lift float64
	}
	var trows []trow
	otherBlocks := len(ix.Blocks) - vacBlocks
	for name, c := range byType {
		rv := float64(c.vac) / float64(vacBlocks) * 100
		ro := float64(c.other) / float64(otherBlocks) * 100
		lift := math.Inf(1) // seen only on vacant blocks
		if ro > 0 {
			lift = rv / ro
		}
		trows = append(trows, trow{name, c.vac, c.other, rv, ro, lift})
	}
	// Map iteration order is random: always break ties by name so the report
	// is reproducible run to run.
	sort.Slice(trows, func(i, j int) bool {
		if trows[i].lift != trows[j].lift {
			return trows[i].lift > trows[j].lift
		}
		return trows[i].name < trows[j].name
	})
	ttbl := report.Table{
		Title:       "Blight request types: rate per 100 blocks, vacant vs. other blocks",
		Headers:     []string{"311 request type", "On vacant blocks", "Per 100 vacant blks", "Per 100 other blks", "Lift"},
		NumericCols: map[int]bool{1: true, 2: true, 3: true, 4: true},
	}
	for _, t := range trows {
		ttbl.Rows = append(ttbl.Rows, []string{t.name, report.Int(t.vac), report.F1(t.rv), report.F1(t.ro), liftStr(t.lift)})
	}

	topType := ""
	if len(trows) > 0 {
		topType = trows[0].name
	}
	return []report.Element{
		report.Para{Text: fmt.Sprintf("Blight = resident-reported sanitation and nuisance-property 311 types (city 'Proactive' sweeps excluded). %s blight requests were geocoded to a hundred-block and joined to %s open vacant notices on the same block key.",
			report.Int(totalBlight), report.Int(len(ds.Vacants)))},
		tbl, bars, ttbl,
		report.Finding{Text: fmt.Sprintf("Blocks with vacant buildings are %s of all blocks but generate %s of resident-reported blight requests. A block with 10+ vacants averages %.1f× the blight requests of a block with none.",
			report.Pct(ratio(float64(vacBlocks), float64(len(ix.Blocks)))), report.Pct(ratio(float64(vacBlight), float64(totalBlight))), ratio(rates[4], rates[0]))},
		report.Finding{Text: fmt.Sprintf("'%s' has the largest over-representation on vacant blocks. Scheduling proactive boarding, cleaning and rat-abatement crews from the vacant-notice list would meet that demand before residents have to call it in.", topType)},
	}
}

func liftStr(l float64) string {
	if math.IsInf(l, 1) {
		return "only vacant"
	}
	return fmt.Sprintf("%.1f×", l)
}
