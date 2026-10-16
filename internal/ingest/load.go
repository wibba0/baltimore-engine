package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aidan/baltimore-engine/internal/model"
	"github.com/aidan/baltimore-engine/internal/normalize"
)

// Candidate file names for each dataset, in preference order. The plain
// files written by `fetch` win over the gzip snapshot bundled in the repo.
var (
	CrimeFiles   = []string{"crime_2025.csv", "crime_2025.csv.gz"}
	RequestFiles = []string{"311_requests_2025.csv", "311_requests_2025.csv.gz"}
	LiquorFiles  = []string{"liquor_licenses.csv", "liquor_licenses.csv.gz"}
	VacantFiles  = []string{"vacant_building_notices.geojson", "vacant_building_notices.geojson.gz"}
)

func resolve(dir string, names []string) (string, error) {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("none of %v found in %s (run `./run.sh fetch` to download)", names, dir)
}

// Available reports whether dir holds a complete set of the four datasets
// (in plain or gzipped form).
func Available(dir string) bool {
	for _, names := range [][]string{CrimeFiles, RequestFiles, LiquorFiles, VacantFiles} {
		if _, err := resolve(dir, names); err != nil {
			return false
		}
	}
	return true
}

// loadOne opens a file through the Source factory and runs the pipeline.
func loadOne[T any](ctx context.Context, dir string, names []string, label string, workers int,
	mk func(*Header) ParseFunc[T], key func(*T) string) ([]T, model.IngestStats, error) {

	st := model.IngestStats{Name: label}
	path, err := resolve(dir, names)
	if err != nil {
		return nil, st, err
	}
	st.File = filepath.Base(path)
	src, err := Open(path)
	if err != nil {
		return nil, st, err
	}
	defer src.Close()
	recs, err := Run(ctx, src, workers, mk, &st)
	if err != nil {
		return nil, st, fmt.Errorf("%s: %w", label, err)
	}
	for i := range recs {
		if key(&recs[i]) != "" {
			st.Matched++
		}
	}
	return recs, st, nil
}

// LoadAll ingests all four datasets concurrently. Each dataset gets its own
// goroutine (and internally its own reader + worker pool), so the 13 MB crime
// file, 7 MB liquor file and GeoJSON vacants finish while the ~280 MB 311
// file is still streaming. Each goroutine writes only to its own variables,
// so no locking is needed; wg.Wait() provides the happens-before edge that
// makes those writes visible to the caller.
func LoadAll(ctx context.Context, dir string, year, workers int) (*model.Dataset, *normalize.Interner, error) {
	in := normalize.NewInterner()
	p := &Parsers{Year: year, In: in}
	ds := &model.Dataset{Year: year}
	stats := make([]model.IngestStats, 4)
	errs := make([]error, 4)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		ds.Crimes, stats[0], errs[0] = loadOne(ctx, dir, CrimeFiles, "Crime (NIBRS Group A)", workers, p.Crime,
			func(c *model.Crime) string { return c.Block })
	}()
	go func() {
		defer wg.Done()
		ds.Requests, stats[1], errs[1] = loadOne(ctx, dir, RequestFiles, "311 Service Requests", workers, p.ServiceRequest,
			func(r *model.ServiceRequest) string { return r.Block })
	}()
	go func() {
		defer wg.Done()
		var recs []model.LiquorLicense
		recs, stats[2], errs[2] = loadOne(ctx, dir, LiquorFiles, "Liquor Licenses", workers, p.Liquor,
			func(l *model.LiquorLicense) string { return l.Block })
		ds.Liquor = DedupeLiquor(recs)
		stats[2].Kept = int64(len(ds.Liquor))
		stats[2].Matched = 0
		for _, l := range ds.Liquor {
			if l.Block != "" {
				stats[2].Matched++
			}
		}
	}()
	go func() {
		defer wg.Done()
		ds.Vacants, stats[3], errs[3] = loadOne(ctx, dir, VacantFiles, "Vacant Building Notices", workers, p.Vacant,
			func(v *model.VacantNotice) string { return v.Block })
	}()
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, nil, err
		}
	}
	ds.Stats = stats
	return ds, in, nil
}
