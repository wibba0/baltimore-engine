package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aidan/baltimore-engine/internal/report"
)

func runFixture(t *testing.T, workers int, kinds ...string) (*Result, map[string]string) {
	t.Helper()
	b := NewBuilder().DataDir("testdata").Year(2025).Workers(workers).AsOf(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	bufs := map[string]*bytes.Buffer{}
	for _, k := range kinds {
		r, err := report.NewRenderer(k)
		if err != nil {
			t.Fatal(err)
		}
		bufs[k] = &bytes.Buffer{}
		b.Output(r, bufs[k])
	}
	e, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range bufs {
		out[k] = v.String()
	}
	return res, out
}

func TestEndToEndJoins(t *testing.T) {
	res, out := runFixture(t, 4, "plain", "terminal", "html")
	ds, ix := res.Dataset, res.Index

	if len(ds.Crimes) != 6 { // 2024 row filtered out
		t.Errorf("crimes = %d, want 6", len(ds.Crimes))
	}
	if len(ds.Requests) != 5 { // soft-deleted row filtered out
		t.Errorf("requests = %d, want 5", len(ds.Requests))
	}
	if len(ds.Liquor) != 1 { // 2 licensee rows → 1 license; 2024 license filtered
		t.Errorf("liquor = %d, want 1", len(ds.Liquor))
	}
	if len(ds.Vacants) != 3 {
		t.Errorf("vacants = %d, want 3", len(ds.Vacants))
	}

	// Cross-dataset block join: liquor "3208-10 GREENMOUNT AVENUE" ↔ crime "3200 GREENMOUNT AVE".
	b := ix.Blocks["3200 GREENMOUNT AVE"]
	if b == nil || b.Liquor != 1 || b.Crimes != 3 || b.Violent != 2 || b.Vacants != 1 || b.Blight != 1 {
		t.Fatalf("greenmount block = %+v", b)
	}
	if b.Neighborhood != "ABELL" {
		t.Errorf("block neighborhood = %q", b.Neighborhood)
	}
	w := ix.Blocks["2100 WILKENS AVE"]
	if w == nil || w.Vacants != 2 || w.Blight != 2 || w.Violent != 2 {
		t.Fatalf("wilkens block = %+v", w)
	}

	// Neighborhood join + council assignment (crime has no district field).
	n := ix.Neighborhoods["CARROLLTON RIDGE"]
	if n == nil || n.Council() != 9 || n.Violent != 2 || n.Vacants != 2 {
		t.Fatalf("carrollton = %+v", n)
	}
	if ix.Neighborhoods["ABELL"].Liquor != 1 {
		t.Error("liquor not rolled up to neighborhood via block")
	}
	// Overdue: 25-2 closed after due date.
	if n.PastDue != 1 || n.DueKnown != 2 {
		t.Errorf("past due = %d/%d", n.PastDue, n.DueKnown)
	}

	for kind, s := range out {
		for q := 1; q <= 5; q++ {
			if !strings.Contains(s, "Q"+string(rune('0'+q))+".") {
				t.Errorf("%s output missing Q%d", kind, q)
			}
		}
	}
	if !strings.HasPrefix(out["html"], "<!doctype html>") {
		t.Error("html renderer did not produce a document")
	}
	if strings.Contains(out["plain"], "\033[") {
		t.Error("plain renderer emitted ANSI escapes")
	}
}

// TestDeterministicAcrossWorkerCounts guards the concurrency design: the
// report must be byte-identical regardless of parallelism (timings aside).
func TestDeterministicAcrossWorkerCounts(t *testing.T) {
	strip := func(s string) string {
		var keep []string
		for _, l := range strings.Split(s, "\n") {
			if strings.Contains(l, "computed in") || strings.Contains(l, " ms") || strings.Contains(l, "ms ") ||
				strings.Contains(l, "MB") || strings.Contains(l, "µs") || strings.Contains(l, "GC") ||
				strings.Contains(l, "Worker goroutines") || strings.Contains(l, "generated") || strings.Contains(l, "End-to-end") {
				continue
			}
			keep = append(keep, l)
		}
		return strings.Join(keep, "\n")
	}
	_, a := runFixture(t, 1, "plain")
	_, b := runFixture(t, 7, "plain")
	if strip(a["plain"]) != strip(b["plain"]) {
		t.Error("report differs between 1 and 7 workers")
	}
}
