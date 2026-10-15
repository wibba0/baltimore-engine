package analysis

import (
	"fmt"
	"math"
	"sort"

	"github.com/aidan/baltimore-engine/internal/index"
	"github.com/aidan/baltimore-engine/internal/report"
)

// ServiceEquity — Q3: joins 311 request lifecycles with crime levels by
// neighborhood to test whether city services are slower where public safety
// is worst.
type ServiceEquity struct{}

func (ServiceEquity) Number() int   { return 3 }
func (ServiceEquity) Title() string { return "311 Service Equity Across Crime Levels" }
func (ServiceEquity) Stakeholder() string {
	return "Mayor's Office of Performance & Innovation (CitiStat)"
}
func (ServiceEquity) Question() string {
	return "Are residents in the highest-crime neighborhoods getting slower or less reliable 311 service (time-to-close, overdue rate) than residents in the safest neighborhoods?"
}

const minFieldRequests = 100

func (ServiceEquity) Run(in *Input) []report.Element {
	var nbs []*index.Neighborhood
	for _, n := range in.IX.Neighborhoods {
		if n.Name != "" && n.FieldRequests >= minFieldRequests {
			nbs = append(nbs, n)
		}
	}
	sort.Slice(nbs, func(i, j int) bool { return nbs[i].Name < nbs[j].Name })
	viol := make([]float64, len(nbs))
	med := make([]float64, len(nbs))
	for i, n := range nbs {
		viol[i] = float64(n.Violent)
		med[i] = quantile32(n.CloseDays, .5)
	}
	quart := quartileOf(viol)

	type qagg struct {
		nbhds, req, closed, pastDue, dueKnown, violent int
		days                                           []float32
	}
	qs := make([]qagg, 4)
	for i, n := range nbs {
		q := &qs[quart[i]]
		q.nbhds++
		q.req += n.FieldRequests
		q.closed += n.FieldClosed
		q.pastDue += n.PastDue
		q.dueKnown += n.DueKnown
		q.violent += n.Violent
		q.days = append(q.days, n.CloseDays...)
	}
	names := []string{"Q1 lowest crime", "Q2", "Q3", "Q4 highest crime"}
	tbl := report.Table{
		Title:       "Field-service 311 requests by neighborhood violent-crime quartile",
		Headers:     []string{"Crime quartile", "Nbhds", "Violent", "Requests", "Closed", "Median days", "P90 days", "Overdue"},
		NumericCols: map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true},
	}
	medBars := report.Bars{Title: "Median days to close a field-service 311 request", Format: "%.2f", Unit: " d"}
	odBars := report.Bars{Title: "Share of requests closed late or still open past due date", Format: "%.1f", Unit: "%"}
	medians := make([]float64, 4)
	overdue := make([]float64, 4)
	for i, q := range qs {
		medians[i] = quantile32(q.days, .5)
		p90 := quantile32(q.days, .9)
		overdue[i] = ratio(float64(q.pastDue), float64(q.dueKnown))
		tbl.Rows = append(tbl.Rows, []string{names[i], report.Int(q.nbhds), report.Int(q.violent), report.Int(q.req),
			report.Pct(ratio(float64(q.closed), float64(q.req))), report.F2(medians[i]), report.F1(p90), report.Pct(overdue[i])})
		medBars.Labels = append(medBars.Labels, names[i])
		medBars.Values = append(medBars.Values, medians[i])
		odBars.Labels = append(odBars.Labels, names[i])
		odBars.Values = append(odBars.Values, overdue[i]*100)
	}

	rho := spearman(viol, med)

	// Slowest neighborhoods.
	idx := make([]int, len(nbs))
	for i := range idx {
		idx[i] = i
	}
	// Stable sort over name-ordered neighborhoods ⇒ ties resolve alphabetically.
	sort.SliceStable(idx, func(a, b int) bool { return med[idx[a]] > med[idx[b]] })
	slow := report.Table{
		Title:       fmt.Sprintf("10 slowest-served neighborhoods (≥%d field requests)", minFieldRequests),
		Headers:     []string{"#", "Neighborhood", "Median days", "Overdue", "Requests", "Violent", "Crime quartile"},
		NumericCols: map[int]bool{0: true, 2: true, 3: true, 4: true, 5: true},
	}
	for r := 0; r < min(10, len(idx)); r++ {
		n := nbs[idx[r]]
		slow.Rows = append(slow.Rows, []string{fmt.Sprint(r + 1), report.Name(n.Name), report.F2(med[idx[r]]),
			report.Pct(ratio(float64(n.PastDue), float64(n.DueKnown))), report.Int(n.FieldRequests), report.Int(n.Violent), names[quart[idx[r]]]})
	}

	gap := ratio(medians[3]-medians[0], medians[0])
	var speed string
	switch {
	case math.Abs(gap) < 0.15:
		speed = fmt.Sprintf("close at roughly the same speed as those in the lowest-crime quartile (median %.2f vs %.2f days, %+.0f%%)", medians[3], medians[0], gap*100)
	case gap > 0:
		speed = fmt.Sprintf("take %.0f%% longer to close than those in the lowest-crime quartile (median %.2f vs %.2f days)", gap*100, medians[3], medians[0])
	default:
		speed = fmt.Sprintf("close %.0f%% faster than those in the lowest-crime quartile (median %.2f vs %.2f days)", -gap*100, medians[3], medians[0])
	}
	var verdict string
	switch {
	case gap >= 0.15 && overdue[3] > overdue[0]:
		verdict = "High-crime neighborhoods are both slower and more often overdue: an equity gap CitiStat should track."
	case gap >= 0.15:
		verdict = fmt.Sprintf("Mixed signal. The typical ticket in a high-crime neighborhood waits somewhat longer, but fewer miss their due date (%s vs %s). That pattern points to a different mix of request types more than to neglect, so CitiStat should monitor it rather than treat it as an equity failure.",
			report.Pct(overdue[3]), report.Pct(overdue[0]))
	default:
		verdict = "No evidence of a systematic service penalty for high-crime neighborhoods."
		if overdue[3] < overdue[0] {
			verdict += " Overdue rates actually run highest in the lowest-crime quartile, so CitiStat should look at lower-volume neighborhoods, where late tickets may draw less attention."
		}
	}
	return []report.Element{
		report.Para{Text: fmt.Sprintf("Field-service requests exclude call-center information requests (ECC-*), city-initiated 'Proactive' sweeps and BGE-owned streetlight tickets, so only work that residents requested and that an agency had to complete is counted. %d neighborhoods with ≥%d such requests are split into four equal groups (quartiles) by their 2025 violent-crime count.",
			len(nbs), minFieldRequests)},
		tbl, medBars, odBars, slow,
		report.Finding{Text: fmt.Sprintf("Requests in the highest-crime quartile %s. Their overdue rate is %s vs %s. Across neighborhoods, the rank correlation between violent crime and median close time is ρ = %.2f (%s).",
			speed, report.Pct(overdue[3]), report.Pct(overdue[0]), rho, strength(rho))},
		report.Finding{Text: verdict},
	}
}
