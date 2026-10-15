package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/aidan/baltimore-engine/internal/report"
)

// LiquorHotspots — Q2: joins Liquor Board licenses to crime by hundred-block.
// Licenses carry only a street address, so the join depends entirely on
// normalize.BlockKey reconciling "6412-16 FRANKFORD AVENUE" with BPD's
// "6400 FRANKFORD AVE".
type LiquorHotspots struct{}

func (LiquorHotspots) Number() int   { return 2 }
func (LiquorHotspots) Title() string { return "Liquor Outlets as Crime Hotspots" }
func (LiquorHotspots) Stakeholder() string {
	return "Board of Liquor License Commissioners & Baltimore Police Department"
}
func (LiquorHotspots) Question() string {
	return "Do blocks with a liquor license see more crime than other blocks? Which license types and which specific outlets sit on the highest-violence blocks? And does crime on those blocks happen during late-night trading hours?"
}

func (LiquorHotspots) Run(in *Input) []report.Element {
	ix, ds := in.IX, in.DS

	// Block-level comparison.
	var lb, ob, lbCrime, obCrime, lbViol, obViol int
	for _, b := range ix.Blocks {
		if b.Liquor > 0 {
			lb++
			lbCrime += b.Crimes
			lbViol += b.Violent
		} else {
			ob++
			obCrime += b.Crimes
			obViol += b.Violent
		}
	}
	lCR, oCR := ratio(float64(lbCrime), float64(lb)), ratio(float64(obCrime), float64(ob))
	lVR, oVR := ratio(float64(lbViol), float64(lb)), ratio(float64(obViol), float64(ob))

	// By establishment type (unique blocks per type).
	type agg struct {
		licenses int
		blocks   map[string]bool
	}
	types := map[string]*agg{}
	matched := 0
	for _, l := range ds.Liquor {
		if l.Block == "" {
			continue
		}
		if _, ok := ix.Blocks[l.Block]; ok && ix.Blocks[l.Block].Crimes > 0 {
			matched++
		}
		t := l.Establishment
		if t == "" {
			t = "(unspecified)"
		}
		a := types[t]
		if a == nil {
			a = &agg{blocks: map[string]bool{}}
			types[t] = a
		}
		a.licenses++
		a.blocks[l.Block] = true
	}
	type trow struct {
		name            string
		lic, blocks     int
		violPB, crimePB float64
	}
	var trows []trow
	for name, a := range types {
		if a.licenses < 15 {
			continue
		}
		v, c := 0, 0
		for k := range a.blocks {
			v += ix.Blocks[k].Violent
			c += ix.Blocks[k].Crimes
		}
		trows = append(trows, trow{name, a.licenses, len(a.blocks), float64(v) / float64(len(a.blocks)), float64(c) / float64(len(a.blocks))})
	}
	sort.Slice(trows, func(i, j int) bool {
		if trows[i].violPB != trows[j].violPB {
			return trows[i].violPB > trows[j].violPB
		}
		return trows[i].name < trows[j].name
	})
	typeBars := report.Bars{Title: "Violent crimes per block, by license establishment type (≥15 licenses)", Format: "%.2f"}
	for _, t := range trows {
		typeBars.Labels = append(typeBars.Labels, fmt.Sprintf("%s (%d)", t.name, t.lic))
		typeBars.Values = append(typeBars.Values, t.violPB)
	}
	typeBars.Labels = append(typeBars.Labels, "Blocks with no liquor license")
	typeBars.Values = append(typeBars.Values, oVR)

	// Top outlet blocks by violent crime.
	type orow struct {
		block string
		names []string
		lic   int
	}
	byBlock := map[string]*orow{}
	for _, l := range ds.Liquor {
		if l.Block == "" {
			continue
		}
		o := byBlock[l.Block]
		if o == nil {
			o = &orow{block: l.Block}
			byBlock[l.Block] = o
		}
		o.lic++
		if len(o.names) < 2 && l.TradeName != "" && !contains(o.names, l.TradeName) {
			o.names = append(o.names, l.TradeName)
		}
	}
	var orows []*orow
	for _, o := range byBlock {
		orows = append(orows, o)
	}
	sort.Slice(orows, func(i, j int) bool {
		a, b := ix.Blocks[orows[i].block], ix.Blocks[orows[j].block]
		if a.Violent != b.Violent {
			return a.Violent > b.Violent
		}
		return orows[i].block < orows[j].block
	})
	top := report.Table{
		Title:       "Top 10 liquor-licensed blocks by violent crime (2025)",
		Headers:     []string{"#", "Block", "Neighborhood", "Licenses", "Example outlet(s)", "Violent", "All crime"},
		NumericCols: map[int]bool{0: true, 3: true, 5: true, 6: true},
	}
	for i := 0; i < min(10, len(orows)); i++ {
		o := orows[i]
		b := ix.Blocks[o.block]
		top.Rows = append(top.Rows, []string{fmt.Sprint(i + 1), o.block, report.Name(b.Neighborhood), report.Int(o.lic),
			truncateStr(strings.Join(o.names, "; "), 34), report.Int(b.Violent), report.Int(b.Crimes)})
	}

	// Hour-of-day profile: liquor blocks vs everywhere else.
	var hA, hB [24]float64
	var nA, nB float64
	for i := range ds.Crimes {
		c := &ds.Crimes[i]
		if c.Block == "" {
			continue
		}
		if b := ix.Blocks[c.Block]; b != nil && b.Liquor > 0 {
			hA[c.Hour]++
			nA++
		} else {
			hB[c.Hour]++
			nB++
		}
	}
	hist := report.Histogram{Title: "Share of each group's crime by hour of day (local time)",
		NameA: "Liquor-licensed blocks", NameB: "All other blocks"}
	lateA, lateB := 0.0, 0.0
	for h := 0; h < 24; h++ {
		hist.Buckets = append(hist.Buckets, fmt.Sprintf("%02d", h))
		hist.A = append(hist.A, hA[h]/nA*100)
		hist.B = append(hist.B, hB[h]/nB*100)
		if h >= 22 || h < 3 {
			lateA += hA[h]
			lateB += hB[h]
		}
	}

	withBlock := 0
	for _, l := range ds.Liquor {
		if l.Block != "" {
			withBlock++
		}
	}
	return []report.Element{
		report.Para{Text: fmt.Sprintf("Join: %s active %d liquor licenses → %s parseable street addresses → %s distinct hundred-blocks; %s licenses (%s) sit on a block with at least one 2025 crime.",
			report.Int(len(ds.Liquor)), ds.Year, report.Int(withBlock), report.Int(lb), report.Int(matched), report.Pct(ratio(float64(matched), float64(withBlock))))},
		report.Table{
			Title:       "Liquor-licensed blocks vs. all other blocks",
			Headers:     []string{"Block type", "Blocks", "Crime / block", "Violent / block"},
			NumericCols: map[int]bool{1: true, 2: true, 3: true},
			Rows: [][]string{
				{"Liquor-licensed", report.Int(lb), report.F2(lCR), report.F2(lVR)},
				{"No liquor license", report.Int(ob), report.F2(oCR), report.F2(oVR)},
				{"Ratio", "", fmt.Sprintf("%.1f×", ratio(lCR, oCR)), fmt.Sprintf("%.1f×", ratio(lVR, oVR))},
			},
		},
		typeBars,
		top,
		hist,
		report.Finding{Text: fmt.Sprintf("Liquor-licensed blocks average %.1f× the violent crime of unlicensed blocks. %s of their crime falls between 22:00 and 03:00, compared with %s on other blocks.",
			ratio(lVR, oVR), report.Pct(lateA/nA), report.Pct(lateB/nB))},
		report.Finding{Text: timing(lateA/nA - lateB/nB)},
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// timing turns the late-night share difference into a recommendation.
func timing(diff float64) string {
	if math.Abs(diff) < 0.02 {
		return "The hour-of-day profile on liquor blocks almost exactly matches the rest of the city, so the excess is about place, not closing time. Restricting trading hours alone is unlikely to help. Premises-level conditions (security plans, lighting, loitering enforcement) at the watch-list outlets above are the better lever."
	}
	if diff > 0 {
		return fmt.Sprintf("Crime on liquor blocks skews %.1f points more toward late night. Earlier closing hours or late-night patrol coverage at the watch-list outlets above are justified.", diff*100)
	}
	return fmt.Sprintf("Crime on liquor blocks is %.1f points *less* concentrated late at night than elsewhere, so daytime conditions at these outlets deserve as much attention as closing time.", -diff*100)
}
