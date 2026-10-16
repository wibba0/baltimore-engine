# Baltimore Multi-Dataset Intelligence Engine (Go)

COSC 352 Mid-Term Capstone: a multi-paradigm data processing engine.

This program **ingests four raw public datasets from the [Baltimore City Open Data Portal](https://data.baltimorecity.gov/)**, about 1 million records in two raw formats (CSV and GeoJSON). It normalizes and **joins them across datasets** on a shared neighborhood key and a shared hundred-block address key, then **answers five research questions for city stakeholders**. The output is a terminal report with ASCII/Unicode charts, plus an optional self-contained HTML web report.

It is written in **Go 1.22+ using only the standard library**: no third-party modules, nothing to `go get`, and builds offline.

There are two ways to use it:
- **Command line** (`./run.sh`): prints the full report in the terminal. Needs only Go.
- **Desktop app** (`Baltimore Engine.app`): a double-clickable macOS app with an interactive dashboard. See [§1a](#1a-desktop-app-macos).

---

## 1. Quick start (macOS)

```bash
# 0) Install Go once, if `go version` fails (either option works):
brew install go                 # Homebrew
#   or download the macOS .pkg from https://go.dev/dl/ and double-click it

# 1) From the project folder:
cd "COSC 352 Midterm Capstone"
./run.sh                        # builds ./bin/baltimore-engine and prints the full report
```

That's it. The repository ships with a gzipped snapshot of all four datasets in `data/`, so **no network access is needed** to build or run.

| Command | What it does |
|---|---|
| `./run.sh` | Build, then print the full colorized report to the terminal |
| `./run.sh html` | Build, write `report.html` (web UI version of the report), and open it in your browser |
| `./run.sh ui` | Open the interactive dashboard in your web browser (Ctrl-C to stop) |
| `./run.sh app` | Build the macOS desktop app, `dist/Baltimore Engine.app` (see §1a) |
| `./run.sh test` | `go vet` plus all unit and integration tests **with the race detector** |
| `./run.sh bench` | Time the ingest stage at 1, 2, 4 and 8 worker goroutines |
| `./run.sh unpack` | Gunzip the snapshot. Plain files make ingest fully parallel (see §6) |
| `./run.sh fetch` | Re-download fresh raw data from Open Baltimore (needs internet, ~2 min) |
| `./run.sh build` | Compile only, to `./bin/baltimore-engine` |
| `./run.sh -- <flags>` | Build, then run with custom flags (below) |

`make build | run | html | test | bench | fetch | clean` are equivalent wrappers.

### Manual build (without the script)

```bash
go build -o bin/baltimore-engine ./cmd/baltimore-engine
./bin/baltimore-engine                       # same as `run`
./bin/baltimore-engine run -h                # list all flags
```

### CLI flags (`baltimore-engine run`)

| Flag | Default | Meaning |
|---|---|---|
| `-data DIR` | `data` | Folder containing the raw dataset files |
| `-year N` | `2025` | Analysis calendar year (also the liquor license year) |
| `-workers N` | #CPUs | Goroutines per parse/index worker pool |
| `-format F` | `terminal` | Stdout renderer: `terminal` (ANSI + Unicode), `plain` (pure ASCII), `html` |
| `-html FILE` | — | Also write the HTML report to FILE |
| `-out FILE` | — | Also write a plain-text report to FILE |
| `-only 1,3` | all | Run only selected questions |
| `-gc PCT` | runtime | Set `GOGC` for the run (GC tuning experiments) |
| `-q` | off | Hide the per-stage timing/heap log on stderr |

---

## 1a. Desktop app (macOS)

`Baltimore Engine.app` is a native macOS application. It opens its own window, with a Dock icon and standard menus, and hosts an interactive dashboard:

- **Sidebar controls**
  - choose which of the five research questions to run
  - set the number of worker goroutines with a slider
  - **Run analysis**
  - download the latest data from Open Baltimore, with live progress
  - open the data folder
  - export the report as a standalone HTML file
- **Tabs**
  - **Overview**: headline metrics, a card per question with its key finding, and data provenance
  - **Q1–Q5**: full tables, bar charts and findings for each question
  - **Performance**: per-stage timing bars, GC statistics, and a run history for comparing worker counts

The app is self-contained: the data snapshot is inside the bundle, and it needs no Go, Terminal or internet to run. It's a universal binary, so it runs on Apple-silicon and Intel Macs with **macOS 13 (Ventura) or newer**.

### Build it

Building needs Go plus the Xcode Command Line Tools. If those are missing, install them with `xcode-select --install`.

```bash
./run.sh app
```

This produces:
- `dist/Baltimore Engine.app`: double-click to run, or drag it to Applications
- `dist/Baltimore-Engine-macOS.zip`: about 59 MB, the file to share or upload

### Opening it on another Mac (Gatekeeper)

The app is **ad-hoc signed, not notarized by Apple** (notarization requires a paid Apple Developer account). The first time someone opens it after downloading, macOS will refuse with a message like *"Apple could not verify 'Baltimore Engine' is free of malware."* To allow it once:

1. Unzip `Baltimore-Engine-macOS.zip` and double-click **Baltimore Engine.app**. Dismiss the warning with **Done**.
2. Open **System Settings → Privacy & Security**, scroll down to the message about "Baltimore Engine", and click **Open Anyway**. Confirm with your password.

After that it opens normally. Alternatively, run this in Terminal to remove the quarantine flag that triggers the warning:

```bash
xattr -dr com.apple.quarantine "Baltimore Engine.app"
```

### How it works

- **`cmd/baltimore-app`** is a small cgo program. It starts the dashboard server (`internal/ui`) on a random `127.0.0.1` port, then runs a Cocoa event loop with an `NSWindow` containing a `WKWebView` (the Safari engine) pointed at that server.
- **Objective-C** (`window_darwin.m`) is limited to the window, the menus and native alert dialogs. All analysis is the same Go engine the CLI uses.
- **`internal/ui`** serves an embedded single-page dashboard and a small JSON API.
  - Every API call requires a random per-launch token, so other programs or web pages can't drive it.
  - Analyses are serialized with a mutex, since each run already uses every core.
  - "Download latest" runs `fetch` in a background goroutine, writing to `~/Library/Application Support/Baltimore Engine/data`. Once that set is complete, it takes precedence over the bundled snapshot.
- **The command-line build stays pure Go.** The app package carries a `darwin && cgo` build constraint, so `./run.sh`, `go build ./...` and `go test ./...` still work on machines without a C compiler.

---

## 2. How to verify it works

1. **Build and run:** `./run.sh` should print the report header, a dataset ingestion table, sections Q1–Q5, and a runtime/memory appendix. It should finish in about 1–2 seconds on an Apple-silicon Mac. A reference copy of the expected output is in [`docs/sample_output.txt`](docs/sample_output.txt).
2. **Tests:** `./run.sh test` should print `ok` for the `analysis`, `engine`, `ingest` and `normalize` packages. These tests cover:
   - **Address normalization:** the same block spelled four ways by four city systems must produce one key.
   - **The Source factory:** CSV, CSV.gz, GeoJSON and GeoJSON.gz.
   - **Both pipeline modes:** they must agree on more than 1 MiB of CSV with embedded newlines and escaped quotes, at 1, 3 and 8 workers.
   - **An end-to-end join test:** runs on small fixture files (`internal/engine/testdata/`).
   - **A determinism test:** the report must be byte-identical whether it runs with 1 or 7 workers.
   - **Race checking:** everything runs under `-race`.
3. **Concurrency:** `./run.sh bench` (then `./run.sh unpack && ./run.sh bench` to see chunked-parse scaling).
4. **Web UI:** `./run.sh html` opens the same report as a responsive HTML page with bar charts and an hour-of-day histogram.
5. **Desktop app:** `./run.sh app`, then double-click `dist/Baltimore Engine.app`. The dashboard runs all five questions automatically on launch.

---

## 3. Datasets (raw inputs)

All four come from Open Baltimore (ArcGIS Hub). `./run.sh fetch` pulls them from the ArcGIS REST services behind the portal and writes them **in the portal's raw export layout**:

- **CSV** with the original column names, and dates as `YYYY/MM/DD HH:MM:SS+00`, exactly like the portal's "Download → CSV".
- **GeoJSON** FeatureCollection for the vacant notices.

The bundled snapshot was fetched on **2026-10-09**.

| Dataset | Portal page | File | Format | Rows |
|---|---|---|---|---|
| NIBRS Group A Crime Data (2025) | [link](https://data.baltimorecity.gov/datasets/204beefe92a645d79fdf0969957bbdf8_0/about) | `crime_2025.csv(.gz)` | CSV | 54,175 |
| 311 Customer Service Requests 2025 | [link](https://data.baltimorecity.gov/datasets/52c763f4496d4101ba0c20af33336a9c_0/about) | `311_requests_2025.csv(.gz)` | CSV | 931,323 |
| Liquor Licenses (all years; engine keeps license year 2025) | [link](https://data.baltimorecity.gov/datasets/ae5ed61365e74579aea25656ac9ce45e_0/about) | `liquor_licenses.csv(.gz)` | CSV | 30,968 |
| Vacant Building Notices (currently open) | [link](https://data.baltimorecity.gov/datasets/691d65a5f85640e6aaa46930bd9dc102_0/about) | `vacant_building_notices.geojson(.gz)` | GeoJSON | 11,396 |

The loader accepts either the plain or the `.gz` file and prefers the plain one if both exist.

### The cross-dataset join problem

The four datasets come from four different city systems, and **none of them share an ID**. The same place is spelled differently in each:

| System | Raw address | Neighborhood | Council district |
|---|---|---|---|
| BPD crime | `3200 GREENMOUNT AVE` | `ABELL` | *(none)* |
| 311 CRM | `3208 GREENMOUNT AVE, Baltimore City, 21218` | `Abell` | `14` |
| DHCD vacants | `3215 GREENMOUNT AVE` | `Abell` | `14` |
| Liquor Board | `3208-10 GREENMOUNT AVENUE` | *(none)* | *(none)* |

`internal/normalize` reduces all of these to the **hundred-block key** `3200 GREENMOUNT AVE` and the **neighborhood key** `ABELL`. Joins that need data a source lacks are done in two hops:

- liquor → block → neighborhood (by majority vote of the crime, 311 and vacant records on that block)
- crime → neighborhood → council district (by majority vote of 311 and vacant records)

Address match rates are shown in the report's ingestion table:

| Dataset | Rows that produce a block key |
|---|---|
| Crime | 99.2% |
| Liquor licenses | 99.7% |
| Vacant notices | 100% |
| 311 requests | 66% |

The 311 rate is lower because call-center information requests have no address.

---

## 4. The five research questions

Each question needs **at least two datasets joined together**. Headline results from the bundled 2025 snapshot:

| # | Stakeholder | Question | Datasets joined | Headline finding |
|---|---|---|---|---|
| **Q1** | Mayor's Office & DHCD | Do vacant buildings concentrate violent crime, and where should demolition and public-safety money be paired? | Vacants × Crime (neighborhood + block) | Spearman ρ = **0.74** between a neighborhood's vacants and its violent crime. Blocks with a vacant building have **5.0×** the violent crime of other blocks. Neighborhoods in the "very high" vacancy tier average 70.6 violent crimes vs 8.1 for neighborhoods with none. |
| **Q2** | Liquor Board & BPD | Are liquor-licensed blocks crime hotspots? Which license types and outlets? Is it a late-night problem? | Liquor × Crime (block key) | Licensed blocks have **4.0×** the violent crime of other blocks. Package-goods stores and hotels/motels are the worst types. The hour-of-day profile matches the rest of the city (21.4% vs 21.3% late-night), so this is **a place problem, not a closing-time problem**. Includes a top-10 outlet watch-list. |
| **Q3** | CitiStat | Do high-crime neighborhoods get worse 311 service? | 311 × Crime (neighborhood) | Mixed signal: median close time is 3.22 vs 2.75 days (highest- vs lowest-crime quartile), but the **overdue rate is lower** (22.4% vs 29.2%). Correlation is weak (ρ = 0.27). There is no clear service penalty. |
| **Q4** | DPW & DHCD Code Enforcement | How much resident-reported blight comes from blocks with vacants? Is there a dose-response? | 311 × Vacants (block key) | Blocks with vacant buildings make up **10.8% of blocks but generate 54.3% of blight requests**. Blight per block climbs steadily from 2.2 (no vacants) to 41.4 (10+ vacants). |
| **Q5** | City Council & Planning | Which council districts carry the heaviest combined burden for the next capital budget? | Crime × Vacants × 311 × Liquor (two-hop joins) | Composite 0–100 score across six metrics. Highest burden: **District 9** (67), **District 7** (59), **District 12** (54). |

Each section in the report includes:
- formatted summary tables
- horizontal bar charts
- (for Q2) a two-series hour-of-day histogram
- plain-language findings, written from the computed numbers rather than hard-coded

---

## 5. Architecture

```
                     ┌──────────────────── ingest (package ingest) ─────────────────────┐
 data/*.csv(.gz) ──▶ │ Factory: Open(path) → Source strategy (CSV | GeoJSON, ±gzip)     │
 data/*.geojson(.gz) │ 4 datasets load concurrently, each through a pipeline:           │
                     │   reader ──chunks/batches──▶ N parse workers ──▶ ordered collector │
                     └──────────────────────────────┬───────────────────────────────────┘
                                                    │ model.Dataset (immutable)
                     ┌──────────────── join / index (package index) ────────────────────┐
                     │ sharded map-reduce: W goroutines build private partial indexes,   │
                     │ merged once → per-neighborhood & per-block aggregates             │
                     └──────────────────────────────┬───────────────────────────────────┘
                                                    │ index.Index (immutable)
                     ┌──────────────── analyze (package analysis) ──────────────────────┐
                     │ 5 Analysis strategies run concurrently, one goroutine each        │
                     └──────────────────────────────┬───────────────────────────────────┘
                                                    │ report.Report (format-neutral)
                     ┌──────────────── render (package report) ─────────────────────────┐
                     │ Renderer strategy: terminal (ANSI/Unicode) | plain ASCII | HTML   │
                     └──────────────────────────────────────────────────────────────────┘
```

### Design patterns used

| Pattern | Where | Why |
|---|---|---|
| **Pipeline** | `ingest.Run*`, `engine.Run` | Ingest → join → analyze → render are separate stages connected by typed, immutable hand-offs. Inside ingest, reader → workers → collector is a channel pipeline. |
| **Factory** | `ingest.Open` (chooses Source by file extension, adds gzip), `report.NewRenderer` | Callers never name a concrete type, so adding Parquet or JSON-lines input, or Markdown output, touches one function. |
| **Strategy** | `ingest.Source` (CSV/GeoJSON decoding), `ingest.ParseFunc` (per-dataset parsing), `analysis.Analysis` (each research question), `report.Renderer` (output format), `index.IsBlight` / `IsFieldService` (311 classification) | Behavior that varies is isolated behind small interfaces. |
| **Builder** | `engine.NewBuilder().DataDir(..).Workers(..).Output(..).Build()` | The CLI, tests and benchmarks each configure only what they need. `Build()` validates the result. |

Separation of concerns:
- **Ingestion** knows nothing about the analyses.
- **Matching logic** lives in `normalize` + `index`.
- **Analyses** never print. They return `report.Element`s that any renderer can format.

---

## 6. Concurrency model

The engine uses only Go's native primitives: goroutines, channels, `sync.WaitGroup`, `sync.Pool`, `sync.RWMutex`, `sync/atomic` and `context`.

1. **Dataset-level parallelism.** `ingest.LoadAll` starts one goroutine per dataset. The small crime, liquor and vacant files finish while the 285 MB 311 file is still streaming. Each goroutine writes only its own result variables, and `wg.Wait()` makes those writes visible to the caller, so no locks are needed.
2. **Chunked parallel CSV parsing** (`ingest.RunChunked`). A naïve CSV pipeline can't parallelize *decoding*, because quoted fields may contain newlines. Here the reader goroutine does **no decoding**. It only scans raw bytes, tracking RFC-4180 quote parity, and cuts the stream into ~1 MiB chunks at newlines that fall *outside* quotes. Each worker runs its own `csv.Reader` on a chunk and applies the dataset's `ParseFunc` (timestamp parsing, float conversion, address normalization, interning). Decoding and parsing therefore both scale with cores. A test checks this against a sequential decoder on data with multi-line quoted fields.
3. **Row-batch mode** (`ingest.RunRows`) for GeoJSON. Rows are decoded sequentially with a streaming `json.Decoder` and fanned out in pooled batches of 4,096 rows.
4. **Bounded channels everywhere** (`workers*2` capacity) give back-pressure, so at most O(workers) chunks are in flight no matter how large the file is.
5. **Ordered fan-in.** Every chunk carries a sequence number, and the collector reassembles in input order. Output is therefore **deterministic regardless of scheduling**; `TestDeterministicAcrossWorkerCounts` enforces this.
6. **Lock-free sharded map-reduce** for the join index (`index.Build`). Each goroutine builds a *private* partial index over a contiguous shard of records, and partials are merged once at the end. This avoids lock contention on roughly 1M map inserts.
7. **Concurrent analyses.** The five questions run in parallel, one goroutine each. Each writes only its own slot of a pre-sized results slice.
8. **Cancellation.** A `context.Context` flows through every stage. A parse error or Ctrl-C cancels the reader and all workers promptly, and every blocking channel send also selects on `ctx.Done()` so no goroutine leaks.
9. **Concurrent HTTP fetch** (`fetch`). Pages are fetched by a bounded worker pool with retry and backoff, then written in order by a single writer, which reorders out-of-order pages through a small buffer map.

**Thread-safety strategy: confinement + immutability.**
- Shared mutable state is avoided rather than locked: workers own their outputs, and `Dataset` and `Index` are never mutated after they are built.
- The only shared mutable structure is the string `Interner`. It uses an `RWMutex` with a read-mostly fast path and double-checked insertion.
- The full test suite and a full-dataset run both pass under `go test -race` / `go build -race`.

**Measured scaling** (Apple M2, 4 performance + 4 efficiency cores, ingest stage):

| Workers | Plain CSV (`./run.sh unpack`) | Bundled `.gz` |
|---|---|---|
| 1 | 1.29 s | 1.29 s |
| 2 | 0.81 s | 1.13 s |
| 4 | **0.68 s (1.9×)** | 1.14 s |
| 8 | 0.76 s | 1.06 s |

With the gzipped snapshot, the stage is bound by **single-stream DEFLATE decompression**, which is inherently sequential, so extra workers help little. Unpacking the files once removes that bottleneck. Beyond 4 workers, the M2's efficiency cores and the four concurrently loading datasets compete for CPU.

---

## 7. Memory and resource management

Go is garbage-collected, so the design focuses on **allocating less, holding less, and making GC scans cheap**:

- **Streaming, never slurping.**
  - Files are read through a 1 MiB `bufio.Reader`, decompressed on the fly with `compress/gzip`, and GeoJSON is decoded one feature at a time.
  - Peak memory doesn't depend on file size; it depends on how many chunks are in flight.
- **Value slices, not pointer graphs.**
  - Records are stored as `[]model.ServiceRequest` (values), not `[]*ServiceRequest`, so about 931k requests sit in one contiguous allocation. The GC scans that linearly instead of chasing 931k pointers.
  - Results are copied once into an exactly-sized slice (`collect`).
- **Compact field types.**
  - Timestamps are `int64` Unix seconds (8 B) instead of `time.Time` (24 B).
  - Coordinates are `float32`, and categories and council districts are `uint8` enums.
- **String interning** (`normalize.Interner`).
  - The 311 file has only about 280 neighborhoods and 400 request types across ~931k rows.
  - Interning makes every record share one backing string, and `strings.Clone` detaches it from the raw CSV line.
  - Without that, each retained substring would pin its whole line in memory. With it, the line buffers become garbage immediately.
  - In total, 29k unique strings serve about 1M records.
- **Buffer reuse with `sync.Pool`.**
  - Raw 1 MiB chunks and row batches are recycled between the reader and the workers.
  - `csv.Reader.ReuseRecord` avoids a `[]string` allocation per row.
- **Releasing memory early.**
  - The collector drops each chunk after copying it.
  - The index deletes its per-block vote maps once neighborhoods are resolved.
- **Measured, not guessed.**
  - The report's appendix prints, per stage: wall time, *live* heap (taken after a forced GC so garbage isn't counted), `TotalAlloc`, `Sys`, GC cycle count and total stop-the-world pause.
  - Typical run: about **138 MB live heap for ~1M joined records**, and under **2 ms total GC pause**.
- **Tunable GC.** `-gc 400` trades memory for fewer collections during the allocation-heavy ingest phase; the appendix shows the effect.
- **Bounded resources.** Fixed-size worker pools (no goroutine-per-row), bounded channels, `defer`-closed files and gzip readers, and an HTTP client with timeouts.

---

## 8. Project layout

```
cmd/baltimore-engine/main.go     CLI: `run` (default), `fetch` and `ui` subcommands
cmd/baltimore-app/               native macOS app: Cocoa window + WKWebView (cgo/Objective-C)
internal/ui/                     dashboard server: JSON API + embedded single-page UI
scripts/build-app.sh             builds, signs and zips "Baltimore Engine.app"
tools/mkicon/                    draws the app icon
internal/fetch/                  concurrent ArcGIS REST downloader → raw CSV / GeoJSON
internal/ingest/
  source.go                      Source strategy + Open() factory (CSV, GeoJSON, gzip)
  pipeline.go                    chunked & row-batch concurrent pipelines, ordered collector
  parsers.go                     per-dataset ParseFuncs (crime, 311, liquor, vacants)
  load.go                        loads all 4 datasets concurrently
internal/normalize/              block-address & neighborhood keys, date parsing, interner
internal/model/                  compact typed records + Dataset
internal/index/                  sharded map-reduce cross-dataset join index
internal/analysis/               Analysis strategy, concurrent runner, stats, Q1–Q5
internal/report/                 format-neutral report model + terminal/plain/HTML renderers
internal/engine/                 Builder + stage orchestration + runtime metrics
data/                            gzipped raw snapshot (2026-10-09)
docs/sample_output.txt           reference output for verification
run.sh, Makefile                 build / run / test / bench / fetch helpers
```

---

## 9. Methodology notes and limitations

- **Time zones.** Crime `CrimeDateTime` and 311 timestamps are published in UTC. The engine converts to `America/New_York` for hour-of-day and calendar-year logic. (The default-midnight reporting spike lands at 04:00/05:00 UTC, which confirms the source is UTC.) The tz database is embedded via `time/tzdata`, so results don't depend on the host machine.
- **Violent crime** = NIBRS homicide (09x), sex offenses (11x), robbery (120), aggravated assault (13A), kidnapping (100) and human trafficking (64x). Common assault and intimidation are tracked separately.
- **Field-service 311 requests** exclude call-center information requests (`ECC-*`), city-initiated "Proactive" sweeps, and BGE-owned streetlight tickets. **Blight** = resident-reported sanitation and nuisance-property types (`index.blightTypes`).
- **Vacant notices** are a snapshot of currently open notices, not notices issued in 2025.
- **Counts, not per-capita rates.** Open Baltimore does not publish population by neighborhood or district, so Q5 ranks the volume of need. Correlations describe association, not causation.
- **Liquor licenses** have one raw row per licensee; the engine de-duplicates to one row per license (class + number).

---

## 10. Troubleshooting

| Symptom | Fix |
|---|---|
| `go: command not found` | Install Go (`brew install go` or the .pkg from https://go.dev/dl/), then open a new terminal |
| `none of [...] found in data` | You're not in the project folder, or `data/` is missing. Run from the repo root, or use `./run.sh fetch` |
| Colors look garbled (e.g. when redirecting to a file) | Use `./run.sh -- -format plain` |
| `fetch` fails | The city's ArcGIS servers are occasionally slow. The fetcher retries with backoff; re-run it, or just use the bundled snapshot |
