package ingest

import (
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aidan/baltimore-engine/internal/model"
)

// BatchSize is the number of decoded rows handed to a worker at once in
// row mode. Batching amortises channel synchronisation (one send per 4096
// rows instead of per row).
const BatchSize = 4096

// ChunkSize is the target size of a raw byte chunk in chunked mode.
const ChunkSize = 1 << 20

// ParseFunc converts one raw row into a typed record. ok=false rejects it.
// A ParseFunc must be safe for concurrent use (it is shared by all workers).
type ParseFunc[T any] func(row []string) (rec T, ok bool)

type chunk[T any] struct {
	seq  int
	rows int
	recs []T
}

// Run streams src through a fan-out/fan-in pipeline and returns the typed
// records in input order. It picks the most parallel strategy the source
// supports:
//
// Chunked mode (CSV): the reader goroutine does no decoding at all. It only
// scans raw bytes, tracking RFC-4180 quote parity, and cuts the stream into
// ~1 MiB chunks at newlines that are *outside* quoted fields. Each worker then
// runs its own csv.Reader and ParseFunc over its chunk, so decoding and
// parsing both scale with cores.
//
// Row mode (GeoJSON or any other Source): one reader decodes rows
// sequentially into pooled batches; workers parse the batches.
//
// In both modes a collector re-assembles results by sequence number, so
// output is deterministic regardless of goroutine scheduling.
func Run[T any](ctx context.Context, src Source, workers int, makeParser func(*Header) ParseFunc[T], st *model.IngestStats) ([]T, error) {
	if c, ok := src.(Chunker); ok {
		return RunChunked(ctx, src, c.Raw(), workers, makeParser, st)
	}
	return RunRows(ctx, src, workers, makeParser, st)
}

// collect drains results, then concatenates chunks in sequence order into
// one exactly-sized slice (a single large allocation the GC scans linearly).
func collect[T any](ctx context.Context, results <-chan chunk[T], readErr *error, st *model.IngestStats, format string, start time.Time) ([]T, error) {
	parts := map[int][]T{}
	total, rows := 0, 0
	for c := range results {
		parts[c.seq] = c.recs
		total += len(c.recs)
		rows += c.rows
	}
	// results is closed only after every worker has exited, and workers exit
	// only after the reader closed its channel, so *readErr is safely visible.
	if *readErr != nil {
		return nil, *readErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]T, 0, total)
	for i := 0; i < len(parts); i++ {
		out = append(out, parts[i]...)
		parts[i] = nil // let the GC reclaim each chunk as soon as it is copied
	}
	if st != nil {
		st.Format = format
		st.Rows = int64(rows)
		st.Kept = int64(total)
		st.Rejected = st.Rows - st.Kept
		st.Millis = time.Since(start).Milliseconds()
	}
	return out, nil
}

// ---- chunked mode ----------------------------------------------------------

type rawChunk struct {
	seq  int
	data []byte
}

var chunkPool = sync.Pool{New: func() any { b := make([]byte, 0, ChunkSize+64<<10); return &b }}

// splitChunks reads r and emits chunks that each end on a record boundary.
func splitChunks(ctx context.Context, r io.Reader, out chan<- rawChunk) error {
	buf := make([]byte, ChunkSize)
	var carry []byte
	inQuote := false
	seq := 0
	emit := func(data []byte) bool {
		bp := chunkPool.Get().(*[]byte)
		b := append((*bp)[:0], data...)
		select {
		case out <- rawChunk{seq: seq, data: b}:
			seq++
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			data := append(carry, buf[:n]...)
			// Scan only the new bytes; quote state already covers the carry.
			cut := -1
			for i := len(carry); i < len(data); i++ {
				switch data[i] {
				case '"':
					inQuote = !inQuote // "" escapes toggle twice: parity preserved
				case '\n':
					if !inQuote {
						cut = i + 1
					}
				}
			}
			if cut < 0 { // one record longer than the buffer: keep accumulating
				carry = data
			} else {
				if !emit(data[:cut]) {
					return ctx.Err()
				}
				carry = append([]byte(nil), data[cut:]...)
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if len(carry) > 0 && !emit(carry) {
				return ctx.Err()
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// RunChunked is the fully parallel CSV path (see Run).
func RunChunked[T any](ctx context.Context, src Source, raw io.Reader, workers int, makeParser func(*Header) ParseFunc[T], st *model.IngestStats) ([]T, error) {
	start := time.Now()
	parse := makeParser(src.Header())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	chunks := make(chan rawChunk, workers*2) // bounded ⇒ back-pressure, O(workers) MiB in flight
	results := make(chan chunk[T], workers*2)
	var readErr error

	go func() {
		defer close(chunks)
		if err := splitChunks(ctx, raw, chunks); err != nil {
			readErr = err
			cancel()
		}
	}()

	var wg sync.WaitGroup
	var decodeErr atomic.Pointer[error]
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			br := bytes.NewReader(nil)
			for c := range chunks {
				br.Reset(c.data)
				cr := csv.NewReader(br)
				cr.ReuseRecord = true
				cr.FieldsPerRecord = -1
				cr.LazyQuotes = true
				out := make([]T, 0, 2048)
				rows := 0
				for {
					rec, err := cr.Read()
					if err == io.EOF {
						break
					}
					if err != nil {
						decodeErr.CompareAndSwap(nil, &err)
						cancel()
						break
					}
					rows++
					if v, ok := parse(rec); ok {
						out = append(out, v)
					}
				}
				// csv.Reader copies field data into its own strings, so the
				// raw chunk buffer can be recycled immediately.
				d := c.data[:0]
				chunkPool.Put(&d)
				select {
				case results <- chunk[T]{seq: c.seq, rows: rows, recs: out}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() { wg.Wait(); close(results) }()

	recs, err := collect(ctx, results, &readErr, st, src.Format(), start)
	if p := decodeErr.Load(); p != nil {
		return nil, *p
	}
	return recs, err
}

// ---- row mode --------------------------------------------------------------

type batch struct {
	seq  int
	rows [][]string
}

// batchPool recycles batches and their row slices. After warm-up the reader
// performs no slice allocations: every row slice is re-filled in place.
var batchPool = sync.Pool{New: func() any {
	return &batch{rows: make([][]string, 0, BatchSize)}
}}

// RunRows is the sequential-decode / parallel-parse path (see Run).
func RunRows[T any](ctx context.Context, src Source, workers int, makeParser func(*Header) ParseFunc[T], st *model.IngestStats) ([]T, error) {
	start := time.Now()
	parse := makeParser(src.Header())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	batches := make(chan *batch, workers*2)
	results := make(chan chunk[T], workers*2)
	var readErr error

	go func() {
		defer close(batches)
		seq := 0
		b := batchPool.Get().(*batch)
		b.rows = b.rows[:0]
		flush := func() bool {
			b.seq = seq
			seq++
			select {
			case batches <- b:
			case <-ctx.Done():
				return false
			}
			b = batchPool.Get().(*batch)
			b.rows = b.rows[:0]
			return true
		}
		for {
			// Re-use the row slice left in this pool slot by a previous batch.
			var dst []string
			if n := len(b.rows); n < cap(b.rows) {
				dst = b.rows[:n+1][n]
			}
			row, err := src.Next(dst)
			if err == io.EOF {
				break
			}
			if err != nil {
				readErr = err
				cancel()
				return
			}
			b.rows = append(b.rows, row)
			if len(b.rows) == BatchSize && !flush() {
				return
			}
		}
		if len(b.rows) > 0 {
			flush()
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range batches {
				out := make([]T, 0, len(b.rows))
				for _, row := range b.rows {
					if rec, ok := parse(row); ok {
						out = append(out, rec)
					}
				}
				c := chunk[T]{seq: b.seq, rows: len(b.rows), recs: out}
				batchPool.Put(b) // row slices keep their capacity for the reader
				select {
				case results <- c:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() { wg.Wait(); close(results) }()

	return collect(ctx, results, &readErr, st, src.Format(), start)
}
