package ingest

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Header maps column names to positions. It is built once per source and
// shared read-only by every worker.
type Header struct {
	Names []string
	index map[string]int
}

func NewHeader(names []string) *Header {
	h := &Header{Names: names, index: make(map[string]int, len(names))}
	for i, n := range names {
		h.index[strings.TrimPrefix(n, "\uFEFF")] = i // strip UTF-8 BOM
	}
	return h
}

// Col returns the column position or -1. Parsers resolve columns once, then
// index rows by position in the hot loop.
func (h *Header) Col(name string) int {
	if i, ok := h.index[name]; ok {
		return i
	}
	return -1
}

// Source streams raw rows from one file. It is the Strategy interface that
// decouples the pipeline from the on-disk format.
type Source interface {
	Format() string
	Header() *Header
	// Next fills dst (reusing its capacity) with the next row's fields.
	// It returns io.EOF when exhausted.
	Next(dst []string) ([]string, error)
	Close() error
}

// Open is the Factory: it picks a Source implementation from the file
// extension, transparently layering gzip decompression for *.gz files.
func Open(path string) (Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var r io.Reader = bufio.NewReaderSize(f, 1<<20)
	name := path
	closers := []io.Closer{f}
	if strings.HasSuffix(name, ".gz") {
		gz, err := gzip.NewReader(r)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		closers = append([]io.Closer{gz}, closers...)
		r = gz
		name = strings.TrimSuffix(name, ".gz")
	}
	var src Source
	switch {
	case strings.HasSuffix(name, ".csv"):
		src, err = newCSVSource(r, closers)
	case strings.HasSuffix(name, ".geojson"), strings.HasSuffix(name, ".json"):
		src, err = newGeoJSONSource(r, closers)
	default:
		err = fmt.Errorf("unsupported format")
	}
	if err != nil {
		for _, c := range closers {
			c.Close()
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return src, nil
}

func closeAll(cs []io.Closer) error {
	var first error
	for _, c := range cs {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ---- CSV -------------------------------------------------------------------

type csvSource struct {
	r       *csv.Reader
	br      *bufio.Reader
	h       *Header
	closers []io.Closer
}

// Chunker is implemented by sources whose remaining bytes can be split into
// independently decodable chunks, enabling fully parallel decoding.
type Chunker interface {
	// Raw returns the undecoded byte stream positioned after the header.
	// A caller must use either Raw or Next, never both.
	Raw() io.Reader
}

func newCSVSource(r io.Reader, closers []io.Closer) (*csvSource, error) {
	// Wrap in our own large bufio.Reader. csv.NewReader re-uses an existing
	// *bufio.Reader instead of adding another, and reads the header only up
	// to its newline, so br is left positioned exactly at the first data row.
	br, ok := r.(*bufio.Reader)
	if !ok || br.Size() < 1<<20 {
		br = bufio.NewReaderSize(r, 1<<20)
	}
	cr := csv.NewReader(br)
	cr.ReuseRecord = true // avoid a []string allocation per row
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	hdr, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}
	return &csvSource{r: cr, br: br, h: NewHeader(append([]string(nil), hdr...)), closers: closers}, nil
}

func (s *csvSource) Raw() io.Reader { return s.br }

func (s *csvSource) Format() string  { return "CSV" }
func (s *csvSource) Header() *Header { return s.h }
func (s *csvSource) Close() error    { return closeAll(s.closers) }
func (s *csvSource) Next(dst []string) ([]string, error) {
	rec, err := s.r.Read()
	if err != nil {
		return dst, err
	}
	// rec's backing array is reused by the reader on the next call, so copy
	// the (immutable) string headers into the caller-owned, pooled slice.
	return append(dst[:0], rec...), nil
}

// ---- GeoJSON ---------------------------------------------------------------

// geoJSONSource streams a FeatureCollection one Feature at a time with a
// json.Decoder, so memory use is O(1 feature) rather than O(file).
// Properties are flattened into string columns plus synthetic "_lon"/"_lat"
// columns taken from Point geometries.
type geoJSONSource struct {
	dec     *json.Decoder
	h       *Header
	pending *feature
	closers []io.Closer
}

type feature struct {
	Geometry *struct {
		Type        string    `json:"type"`
		Coordinates []float64 `json:"coordinates"`
	} `json:"geometry"`
	Properties map[string]json.RawMessage `json:"properties"`
}

func newGeoJSONSource(r io.Reader, closers []io.Closer) (*geoJSONSource, error) {
	dec := json.NewDecoder(r)
	// Walk tokens until we're inside the "features" array.
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("no features array: %w", err)
		}
		switch t := tok.(type) {
		case json.Delim:
			if t == '{' || t == '[' {
				depth++
			} else {
				depth--
			}
		case string:
			if t == "features" && depth == 1 {
				if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
					return nil, fmt.Errorf("features is not an array")
				}
				goto inArray
			}
		}
	}
inArray:
	s := &geoJSONSource{dec: dec, closers: closers}
	var names []string
	if dec.More() {
		var f feature
		if err := dec.Decode(&f); err != nil {
			return nil, err
		}
		for k := range f.Properties {
			names = append(names, k)
		}
		sort.Strings(names)
		s.pending = &f
	}
	s.h = NewHeader(append(names, "_lon", "_lat"))
	return s, nil
}

func (s *geoJSONSource) Format() string  { return "GeoJSON" }
func (s *geoJSONSource) Header() *Header { return s.h }
func (s *geoJSONSource) Close() error    { return closeAll(s.closers) }

func (s *geoJSONSource) Next(dst []string) ([]string, error) {
	var f *feature
	if s.pending != nil {
		f, s.pending = s.pending, nil
	} else {
		if !s.dec.More() {
			return dst, io.EOF
		}
		f = new(feature)
		if err := s.dec.Decode(f); err != nil {
			return dst, err
		}
	}
	n := len(s.h.Names)
	dst = dst[:0]
	for i := 0; i < n-2; i++ {
		dst = append(dst, rawToString(f.Properties[s.h.Names[i]]))
	}
	lon, lat := "", ""
	if f.Geometry != nil && f.Geometry.Type == "Point" && len(f.Geometry.Coordinates) >= 2 {
		lon = strconv.FormatFloat(f.Geometry.Coordinates[0], 'f', -1, 64)
		lat = strconv.FormatFloat(f.Geometry.Coordinates[1], 'f', -1, 64)
	}
	return append(dst, lon, lat), nil
}

func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return string(raw)
}
