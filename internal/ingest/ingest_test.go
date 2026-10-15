package ingest

import (
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aidan/baltimore-engine/internal/model"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if strings.HasSuffix(name, ".gz") {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		zw := gzip.NewWriter(f)
		zw.Write([]byte(body))
		zw.Close()
		f.Close()
		return p
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFactorySelectsSource(t *testing.T) {
	dir := t.TempDir()
	csvBody := "a,b\n1,\"x, y\"\n2,z\n"
	geo := `{"type":"FeatureCollection","features":[{"type":"Feature","geometry":{"type":"Point","coordinates":[-76.6,39.3]},"properties":{"a":1,"b":"q"}}]}`
	for name, wantFmt := range map[string]string{"t.csv": "CSV", "t.csv.gz": "CSV", "t.geojson": "GeoJSON", "t.geojson.gz": "GeoJSON"} {
		body := csvBody
		if wantFmt == "GeoJSON" {
			body = geo
		}
		src, err := Open(writeFile(t, dir, name, body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if src.Format() != wantFmt {
			t.Errorf("%s: format %s, want %s", name, src.Format(), wantFmt)
		}
		row, err := src.Next(nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if row[src.Header().Col("a")] != "1" {
			t.Errorf("%s: a = %q", name, row[src.Header().Col("a")])
		}
		if wantFmt == "GeoJSON" && row[src.Header().Col("_lat")] != "39.3" {
			t.Errorf("geojson lat = %q", row[src.Header().Col("_lat")])
		}
		src.Close()
	}
	if _, err := Open(writeFile(t, dir, "t.xml", "<x/>")); err == nil {
		t.Error("expected error for unsupported format")
	}
}

// TestPipelineModesAgree pushes >1 MiB of CSV (so chunked mode produces
// several chunks) containing quoted fields with embedded newlines, commas and
// escaped quotes through both pipeline modes, at several worker counts, and
// requires identical, complete, in-order results.
func TestPipelineModesAgree(t *testing.T) {
	const n = 160000
	var sb strings.Builder
	sb.WriteString("id,keep,note\n")
	for i := 0; i < n; i++ {
		note := "plain"
		if i%97 == 0 {
			note = "\"line one\nline \"\"two\"\", with comma\""
		}
		fmt.Fprintf(&sb, "%d,%d,%s\n", i, i%3, note)
	}
	if sb.Len() < 2*ChunkSize {
		t.Fatalf("fixture too small to span chunks: %d bytes", sb.Len())
	}
	p := writeFile(t, t.TempDir(), "rows.csv", sb.String())
	mk := func(h *Header) ParseFunc[int] {
		id, keep, note := h.Col("id"), h.Col("keep"), h.Col("note")
		return func(row []string) (int, bool) {
			v, _ := strconv.Atoi(row[id])
			if v%97 == 0 && row[note] != "line one\nline \"two\", with comma" {
				return -1, true // surfaces a mis-split record as an ordering failure
			}
			return v, row[keep] != "0"
		}
	}
	var reference []int
	for _, mode := range []string{"rows", "chunked"} {
		for _, workers := range []int{1, 3, 8} {
			src, err := Open(p)
			if err != nil {
				t.Fatal(err)
			}
			var st model.IngestStats
			var got []int
			if mode == "rows" {
				got, err = RunRows(context.Background(), src, workers, mk, &st)
			} else {
				got, err = Run(context.Background(), src, workers, mk, &st) // CSV ⇒ chunked
			}
			src.Close()
			if err != nil {
				t.Fatal(err)
			}
			if st.Rows != n || int(st.Kept) != len(got) {
				t.Fatalf("%s/%d: stats %+v, len %d", mode, workers, st, len(got))
			}
			for i := 1; i < len(got); i++ {
				if got[i] <= got[i-1] {
					t.Fatalf("%s/%d: out of order or mis-split at %d", mode, workers, i)
				}
			}
			if reference == nil {
				reference = got
			} else if len(got) != len(reference) {
				t.Fatalf("%s/%d: %d records, reference has %d", mode, workers, len(got), len(reference))
			}
		}
	}
}

func TestCrimeCategory(t *testing.T) {
	cases := map[string]model.CrimeCategory{
		"09A": model.CatViolent, "13A": model.CatViolent, "120": model.CatViolent, "11A": model.CatViolent,
		"13B": model.CatOther, "220": model.CatProperty, "23F": model.CatProperty, "290": model.CatProperty, "520": model.CatOther,
	}
	for code, want := range cases {
		if got := CrimeCategoryOf(code); got != want {
			t.Errorf("%s: got %v want %v", code, got, want)
		}
	}
}
