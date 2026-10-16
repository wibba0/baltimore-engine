// Package engine wires the stages together:
//
//	Ingest (parallel, per dataset) → Index (sharded map-reduce)
//	  → Analyze (5 concurrent strategies) → Render (pluggable strategy)
//
// An Engine is assembled with a Builder so the CLI, tests and benchmarks can
// configure exactly the pieces they need.
package engine

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/aidan/baltimore-engine/internal/analysis"
	"github.com/aidan/baltimore-engine/internal/index"
	"github.com/aidan/baltimore-engine/internal/ingest"
	"github.com/aidan/baltimore-engine/internal/model"
	"github.com/aidan/baltimore-engine/internal/report"
)

// Engine runs the pipeline. Build one with NewBuilder().
type Engine struct {
	dataDir   string
	year      int
	workers   int
	gcPercent int
	asOf      time.Time
	analyses  []analysis.Analysis
	renderers []output
	log       io.Writer
}

type output struct {
	r report.Renderer
	w io.Writer
}

// Builder configures an Engine step by step (Builder pattern).
type Builder struct{ e Engine }

func NewBuilder() *Builder {
	return &Builder{e: Engine{
		dataDir:   "data",
		year:      2025,
		workers:   runtime.NumCPU(),
		gcPercent: -2, // sentinel: leave runtime default
		asOf:      time.Now(),
		log:       io.Discard,
	}}
}

func (b *Builder) DataDir(d string) *Builder { b.e.dataDir = d; return b }
func (b *Builder) Year(y int) *Builder       { b.e.year = y; return b }
func (b *Builder) Workers(n int) *Builder {
	if n > 0 {
		b.e.workers = n
	}
	return b
}

// GCPercent sets debug.SetGCPercent for the run (e.g. 400 trades memory for
// fewer collections during the allocation-heavy ingest phase).
func (b *Builder) GCPercent(p int) *Builder                 { b.e.gcPercent = p; return b }
func (b *Builder) AsOf(t time.Time) *Builder                { b.e.asOf = t; return b }
func (b *Builder) Log(w io.Writer) *Builder                 { b.e.log = w; return b }
func (b *Builder) Analyses(a ...analysis.Analysis) *Builder { b.e.analyses = a; return b }
func (b *Builder) Output(r report.Renderer, w io.Writer) *Builder {
	b.e.renderers = append(b.e.renderers, output{r, w})
	return b
}

func (b *Builder) Build() (*Engine, error) {
	e := b.e
	if e.analyses == nil {
		e.analyses = analysis.All()
	}
	if e.workers < 1 {
		return nil, fmt.Errorf("engine: workers must be ≥ 1")
	}
	return &e, nil
}

// Result exposes intermediate products (used by tests and the UI).
type Result struct {
	Dataset *model.Dataset
	Index   *index.Index
	Report  *report.Report
	Metrics Metrics
}

// Metrics summarises one run for dashboards.
type Metrics struct {
	Stages     []Stage
	Total      time.Duration // ingest → analyze, excluding render
	Workers    int
	Records    int
	LiveHeapMB float64
	GCPause    time.Duration
	NumGC      uint32
}

// Stage is the timing and live heap of one pipeline stage.
type Stage struct {
	Name   string
	Dur    time.Duration
	HeapMB float64
}

type stage struct {
	name string
	dur  time.Duration
	heap uint64
}

// Run executes the full pipeline and renders to every configured output.
func (e *Engine) Run(ctx context.Context) (*Result, error) {
	if e.gcPercent != -2 {
		old := debug.SetGCPercent(e.gcPercent)
		defer debug.SetGCPercent(old)
	}
	var stages []stage
	var ms, ms0 runtime.MemStats
	// Runtime counters are cumulative for the process; snapshot them so a
	// long-running host (the dashboard) reports per-run figures.
	runtime.ReadMemStats(&ms0)
	// mark records a stage's wall time, then forces a collection so the
	// reported heap is the *live* working set rather than live + garbage
	// awaiting the next GC cycle. The forced GC is excluded from stage time.
	mark := func(name string, start time.Time) {
		d := time.Since(start)
		runtime.GC()
		runtime.ReadMemStats(&ms)
		stages = append(stages, stage{name, d, ms.HeapAlloc})
		fmt.Fprintf(e.log, "  %-28s %10s   live heap %6.1f MB\n", name, d.Round(time.Millisecond), mb(ms.HeapAlloc))
	}
	t0 := time.Now()

	fmt.Fprintf(e.log, "baltimore-engine: data=%s year=%d workers=%d GOMAXPROCS=%d\n", e.dataDir, e.year, e.workers, runtime.GOMAXPROCS(0))
	t := time.Now()
	ds, interner, err := ingest.LoadAll(ctx, e.dataDir, e.year, e.workers)
	if err != nil {
		return nil, err
	}
	mark("1. ingest (4 datasets, parallel)", t)

	t = time.Now()
	ix := index.Build(ds, e.workers, e.asOf.Unix())
	mark("2. join/index (map-reduce)", t)

	t = time.Now()
	sections := analysis.RunAll(&analysis.Input{DS: ds, IX: ix}, e.analyses)
	mark("3. analyze (5 strategies, parallel)", t)

	rep := &report.Report{
		Title:    "Baltimore Multi-Dataset Intelligence Report",
		Subtitle: fmt.Sprintf("Open Baltimore data · calendar year %d · generated %s", e.year, e.asOf.Format("2006-01-02 15:04")),
		Preamble: provenance(ds),
		Sections: sections,
	}

	runtime.ReadMemStats(&ms)
	total := time.Since(t0)
	rep.Appendix = appendix(stages, total, &ms, &ms0, e.workers, interner.Len(), ds)

	t = time.Now()
	for _, o := range e.renderers {
		if err := o.r.Render(o.w, rep); err != nil {
			return nil, err
		}
	}
	if len(e.renderers) > 0 {
		mark("4. render", t)
	}
	m := Metrics{Total: total, Workers: e.workers, LiveHeapMB: mb(ms.HeapAlloc),
		GCPause: time.Duration(ms.PauseTotalNs - ms0.PauseTotalNs), NumGC: ms.NumGC - ms0.NumGC,
		Records: len(ds.Crimes) + len(ds.Requests) + len(ds.Liquor) + len(ds.Vacants)}
	for _, s := range stages {
		m.Stages = append(m.Stages, Stage{s.name, s.dur, mb(s.heap)})
	}
	return &Result{Dataset: ds, Index: ix, Report: rep, Metrics: m}, nil
}

func mb(b uint64) float64 { return float64(b) / (1 << 20) }

func provenance(ds *model.Dataset) []report.Element {
	tbl := report.Table{
		Title:       "Datasets ingested (raw files → typed records)",
		Headers:     []string{"Dataset", "File", "Format", "Raw rows", "Kept", "Filtered", "Block-geocoded", "Parse time"},
		NumericCols: map[int]bool{3: true, 4: true, 5: true, 6: true, 7: true},
	}
	for _, s := range ds.Stats {
		tbl.Rows = append(tbl.Rows, []string{s.Name, s.File, s.Format, report.Int(int(s.Rows)), report.Int(int(s.Kept)),
			report.Int(int(s.Rows - s.Kept)), report.Pct(float64(s.Matched) / float64(max(s.Kept, 1))), fmt.Sprintf("%d ms", s.Millis)})
	}
	return []report.Element{
		report.Para{Text: "Source: Baltimore City Open Data Portal (data.baltimorecity.gov). Four independently maintained city systems are reconciled on two shared keys, a normalised neighborhood name and a hundred-block street address. All five questions below depend on at least two datasets joined on those keys."},
		tbl,
	}
}

func appendix(stages []stage, total time.Duration, ms, ms0 *runtime.MemStats, workers, interned int, ds *model.Dataset) []report.Element {
	tbl := report.Table{
		Title:       "Pipeline stages",
		Headers:     []string{"Stage", "Wall time", "Live heap after (post-GC)"},
		NumericCols: map[int]bool{1: true, 2: true},
	}
	for _, s := range stages {
		tbl.Rows = append(tbl.Rows, []string{s.name, s.dur.Round(time.Millisecond).String(), fmt.Sprintf("%.1f MB", mb(s.heap))})
	}
	recs := len(ds.Crimes) + len(ds.Requests) + len(ds.Liquor) + len(ds.Vacants)
	mem := report.Table{
		Title:       "Go runtime memory statistics",
		Headers:     []string{"Metric", "Value"},
		NumericCols: map[int]bool{1: true},
		Rows: [][]string{
			{"Worker goroutines per pool", fmt.Sprint(workers)},
			{"GOMAXPROCS (OS threads running Go code)", fmt.Sprint(runtime.GOMAXPROCS(0))},
			{"Typed records held in memory", report.Int(recs)},
			{"Unique interned strings", report.Int(interned)},
			{"Live heap at end (HeapAlloc)", fmt.Sprintf("%.1f MB", mb(ms.HeapAlloc))},
			{"Total allocated over run (TotalAlloc)", fmt.Sprintf("%.1f MB", mb(ms.TotalAlloc-ms0.TotalAlloc))},
			{"Memory obtained from OS (Sys)", fmt.Sprintf("%.1f MB", mb(ms.Sys))},
			{"GC cycles (incl. 1 forced per stage for measurement)", fmt.Sprint(ms.NumGC - ms0.NumGC)},
			{"Total GC stop-the-world pause", time.Duration(ms.PauseTotalNs - ms0.PauseTotalNs).Round(time.Microsecond).String()},
			{"End-to-end time (excl. render)", total.Round(time.Millisecond).String()},
		},
	}
	return []report.Element{tbl, mem}
}
