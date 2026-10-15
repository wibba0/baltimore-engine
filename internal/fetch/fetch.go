// Package fetch downloads raw datasets from the Open Baltimore ArcGIS REST
// services that back https://data.baltimorecity.gov/.
//
// Each layer is paged with resultOffset/resultRecordCount. Pages are fetched
// concurrently by a bounded pool of goroutines (fan-out) and re-ordered by a
// single writer goroutine (fan-in) so the output file is deterministic and
// streamed to disk without holding the whole dataset in memory.
package fetch

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Format is the raw on-disk format a layer is written in.
type Format int

const (
	CSV Format = iota
	GeoJSON
)

// Layer describes one Open Baltimore dataset.
type Layer struct {
	Name     string
	ItemURL  string // human-facing portal page (documentation only)
	URL      string // ArcGIS FeatureServer layer URL
	Where    string
	Fields   []string // nil = all fields
	OrderBy  string   // object id field, required for stable paging
	OutFile  string
	Format   Format
	PageSize int
}

// The crime window is padded by a day on each side because the service
// filters in UTC; the parser trims to the Baltimore-local calendar year.

// Layers is the canonical list of datasets the engine consumes.
var Layers = []Layer{
	{
		Name:    "Part 1 / NIBRS Group A Crime (2025)",
		ItemURL: "https://data.baltimorecity.gov/datasets/204beefe92a645d79fdf0969957bbdf8_0/about",
		URL:     "https://services1.arcgis.com/UWYHeuuJISiGmgXx/ArcGIS/rest/services/NIBRS_GroupA_Crime_Data/FeatureServer/0",
		Where:   "CrimeDateTime >= timestamp '2024-12-31 00:00:00' AND CrimeDateTime < timestamp '2026-01-02 00:00:00'",
		OrderBy: "RowID",
		OutFile: "crime_2025.csv",
		Format:  CSV,
	},
	{
		Name:    "311 Customer Service Requests (2025)",
		ItemURL: "https://data.baltimorecity.gov/datasets/52c763f4496d4101ba0c20af33336a9c_0/about",
		URL:     "https://services1.arcgis.com/UWYHeuuJISiGmgXx/ArcGIS/rest/services/311_Customer_Service_Requests_2025/FeatureServer/0",
		Where:   "1=1",
		Fields: []string{"ServiceRequestNum", "SRType", "MethodReceived", "CreatedDate", "SRStatus",
			"StatusDate", "DueDate", "CloseDate", "Agency", "LastActivity", "Outcome", "Address",
			"ZipCode", "Neighborhood", "CouncilDistrict", "PoliceDistrict", "PolicePost",
			"Latitude", "Longitude", "IsDeleted"},
		OrderBy: "ESRI_OID",
		OutFile: "311_requests_2025.csv",
		Format:  CSV,
	},
	{
		Name:    "Liquor Licenses",
		ItemURL: "https://data.baltimorecity.gov/datasets/ae5ed61365e74579aea25656ac9ce45e_0/about",
		URL:     "https://baltegis.baltimorecity.gov/mapping/rest/services/CityView/Licenses/FeatureServer/0",
		Where:   "1=1",
		OrderBy: "ESRI_OID",
		OutFile: "liquor_licenses.csv",
		Format:  CSV,
	},
	{
		Name:    "Vacant Building Notices (open)",
		ItemURL: "https://data.baltimorecity.gov/datasets/691d65a5f85640e6aaa46930bd9dc102_0/about",
		URL:     "https://baltegis.baltimorecity.gov/mapping/rest/services/Housing/DHCD_Open_Baltimore_Datasets/FeatureServer/1",
		Where:   "1=1",
		OrderBy: "OBJECTID",
		OutFile: "vacant_building_notices.geojson",
		Format:  GeoJSON,
	},
}

// Options controls a fetch run.
type Options struct {
	OutDir  string
	Workers int
	Log     io.Writer
}

var client = &http.Client{Timeout: 90 * time.Second}

// All fetches every layer in Layers, one after another (each layer is
// internally parallel; running layers sequentially keeps load on the city's
// servers polite).
func All(ctx context.Context, opt Options) error {
	if err := os.MkdirAll(opt.OutDir, 0o755); err != nil {
		return err
	}
	for _, l := range Layers {
		if err := Fetch(ctx, l, opt); err != nil {
			return fmt.Errorf("%s: %w", l.Name, err)
		}
	}
	return nil
}

type fieldMeta struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type page struct {
	idx  int
	body []byte // encoded CSV rows or comma-joined GeoJSON features
	rows int
	err  error
}

// Fetch downloads one layer.
func Fetch(ctx context.Context, l Layer, opt Options) error {
	if l.PageSize == 0 {
		l.PageSize = 2000
	}
	if opt.Workers <= 0 {
		opt.Workers = 6
	}
	start := time.Now()

	fields, err := layerFields(ctx, l.URL)
	if err != nil {
		return err
	}
	if l.Fields != nil {
		keep := map[string]bool{}
		for _, f := range l.Fields {
			keep[f] = true
		}
		var sel []fieldMeta
		for _, f := range fields {
			if keep[f.Name] {
				sel = append(sel, f)
			}
		}
		fields = sel
	}
	total, err := count(ctx, l)
	if err != nil {
		return err
	}
	pages := (total + l.PageSize - 1) / l.PageSize
	fmt.Fprintf(opt.Log, "fetch %-40s %8d records, %4d pages, %d workers\n", l.Name, total, pages, opt.Workers)

	tmp := filepath.Join(opt.OutDir, l.OutFile+".part")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)

	// Header.
	if l.Format == CSV {
		cw := csv.NewWriter(w)
		names := make([]string, len(fields))
		for i, fm := range fields {
			names[i] = fm.Name
		}
		cw.Write(names)
		cw.Flush()
	} else {
		w.WriteString(`{"type":"FeatureCollection","features":[` + "\n")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	results := make(chan page, opt.Workers*2)
	var wg sync.WaitGroup
	var done atomic.Int64
	for i := 0; i < opt.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				p := fetchPage(ctx, l, fields, idx)
				done.Add(int64(p.rows))
				select {
				case results <- p:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := 0; i < pages; i++ {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	// Ordered fan-in: buffer out-of-order pages until their turn comes.
	pending := map[int]page{}
	next, written, firstFeature := 0, 0, true
	for p := range results {
		if p.err != nil {
			cancel()
			return p.err
		}
		pending[p.idx] = p
		for {
			q, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			if l.Format == GeoJSON && len(q.body) > 0 {
				if !firstFeature {
					w.WriteString(",\n")
				}
				firstFeature = false
			}
			w.Write(q.body)
			written += q.rows
			next++
		}
		fmt.Fprintf(opt.Log, "\r  %d/%d pages  %d rows", next, pages, done.Load())
	}
	if l.Format == GeoJSON {
		w.WriteString("\n]}\n")
	}
	if err := w.Flush(); err != nil {
		return err
	}
	f.Close()
	fmt.Fprintf(opt.Log, "\r  wrote %s: %d rows in %s\n", l.OutFile, written, time.Since(start).Round(time.Millisecond))
	return os.Rename(tmp, filepath.Join(opt.OutDir, l.OutFile))
}

func getJSON(ctx context.Context, u string, v any) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*attempt) * time.Second)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "baltimore-engine/1.0 (COSC 352 capstone)")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			lastErr = fmt.Errorf("HTTP %d: %v", resp.StatusCode, err)
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(string(body[:min(len(body), 20)])), `{"error"`) {
			lastErr = fmt.Errorf("server error: %s", body[:min(len(body), 300)])
			continue
		}
		if v != nil {
			if err := json.Unmarshal(body, v); err != nil {
				lastErr = err
				continue
			}
		}
		return body, nil
	}
	return nil, lastErr
}

func layerFields(ctx context.Context, layerURL string) ([]fieldMeta, error) {
	var meta struct {
		Fields []fieldMeta `json:"fields"`
	}
	if _, err := getJSON(ctx, layerURL+"?f=json", &meta); err != nil {
		return nil, err
	}
	return meta.Fields, nil
}

func count(ctx context.Context, l Layer) (int, error) {
	q := url.Values{"where": {l.Where}, "returnCountOnly": {"true"}, "f": {"json"}}
	var r struct {
		Count int `json:"count"`
	}
	_, err := getJSON(ctx, l.URL+"/query?"+q.Encode(), &r)
	return r.Count, err
}

func fetchPage(ctx context.Context, l Layer, fields []fieldMeta, idx int) page {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	q := url.Values{
		"where":             {l.Where},
		"outFields":         {strings.Join(names, ",")},
		"orderByFields":     {l.OrderBy},
		"resultOffset":      {strconv.Itoa(idx * l.PageSize)},
		"resultRecordCount": {strconv.Itoa(l.PageSize)},
		"outSR":             {"4326"},
	}
	if l.Format == GeoJSON {
		q.Set("f", "geojson")
		var r struct {
			Features []json.RawMessage `json:"features"`
		}
		if _, err := getJSON(ctx, l.URL+"/query?"+q.Encode(), &r); err != nil {
			return page{idx: idx, err: err}
		}
		var sb strings.Builder
		for i, ft := range r.Features {
			if i > 0 {
				sb.WriteString(",\n")
			}
			sb.Write(ft)
		}
		return page{idx: idx, body: []byte(sb.String()), rows: len(r.Features)}
	}

	q.Set("f", "json")
	q.Set("returnGeometry", "false")
	var r struct {
		Features []struct {
			Attributes map[string]json.RawMessage `json:"attributes"`
		} `json:"features"`
	}
	if _, err := getJSON(ctx, l.URL+"/query?"+q.Encode(), &r); err != nil {
		return page{idx: idx, err: err}
	}
	var sb strings.Builder
	cw := csv.NewWriter(&sb)
	row := make([]string, len(fields))
	for _, ft := range r.Features {
		for i, fm := range fields {
			row[i] = renderValue(ft.Attributes[fm.Name], fm.Type)
		}
		cw.Write(row)
	}
	cw.Flush()
	return page{idx: idx, body: []byte(sb.String()), rows: len(r.Features)}
}

// renderValue mirrors the Open Baltimore CSV export: dates become
// "YYYY/MM/DD HH:MM:SS+00" in UTC, nulls become empty strings.
func renderValue(raw json.RawMessage, typ string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		json.Unmarshal(raw, &s)
		return s
	}
	if typ == "esriFieldTypeDate" {
		ms, err := strconv.ParseInt(string(raw), 10, 64)
		if err == nil {
			return time.UnixMilli(ms).UTC().Format("2006/01/02 15:04:05+00")
		}
	}
	return string(raw)
}
