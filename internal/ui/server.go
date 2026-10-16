// Package ui serves the interactive dashboard: a small JSON API over the
// engine plus an embedded single-page front end. It is used both by the
// native macOS app (cmd/baltimore-app, which shows it in a WebKit window) and
// by `baltimore-engine ui` (which opens it in the default browser).
//
// The server listens on 127.0.0.1 only, on a random port, and every API call
// must carry a per-launch random token, so other local processes or web pages
// cannot drive it.
package ui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aidan/baltimore-engine/internal/analysis"
	"github.com/aidan/baltimore-engine/internal/engine"
	"github.com/aidan/baltimore-engine/internal/fetch"
	"github.com/aidan/baltimore-engine/internal/ingest"
	"github.com/aidan/baltimore-engine/internal/report"
)

//go:embed static/index.html
var static embed.FS

// Config tells the server where data lives.
type Config struct {
	// BundledData is the read-only snapshot shipped with the program.
	BundledData string
	// UserData is a writable folder for fresh downloads; when it holds a
	// complete dataset it takes precedence over the bundled snapshot.
	UserData string
}

// DefaultUserData is ~/Library/Application Support/Baltimore Engine/data.
func DefaultUserData() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "baltimore-engine-data")
	}
	return filepath.Join(dir, "Baltimore Engine", "data")
}

// Server is the dashboard backend.
type Server struct {
	cfg   Config
	token string

	runMu sync.Mutex // one analysis at a time (each run uses every core)
	last  *report.Report
	lastM sync.Mutex

	fetchMu  sync.Mutex
	fetching bool
	fetchLog *lineLog
	fetchErr string
	fetchEnd time.Time
}

func New(cfg Config) *Server {
	b := make([]byte, 16)
	rand.Read(b)
	return &Server{cfg: cfg, token: hex.EncodeToString(b), fetchLog: &lineLog{max: 40}}
}

// Start listens on a random loopback port and returns the URL to open.
func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/info", s.auth(s.info))
	mux.HandleFunc("/api/run", s.auth(s.run))
	mux.HandleFunc("/api/export", s.auth(s.export))
	mux.HandleFunc("/api/fetch", s.auth(s.startFetch))
	mux.HandleFunc("/api/fetch/status", s.auth(s.fetchStatus))
	mux.HandleFunc("/api/reveal-data", s.auth(s.revealData))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	return fmt.Sprintf("http://%s/?t=%s", ln.Addr(), s.token), nil
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != s.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		start := time.Now()
		h(w, r)
		if r.URL.Path != "/api/fetch/status" {
			log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, _ := static.ReadFile("static/index.html")
	page := strings.Replace(string(b), "/*REPORT_CSS*/", report.CSS(), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, page)
}

// dataDir picks fresh downloads when complete, else the bundled snapshot.
func (s *Server) dataDir() (dir, label string) {
	if s.cfg.UserData != "" && ingest.Available(s.cfg.UserData) {
		label = "Downloaded data"
		if fi, err := os.Stat(filepath.Join(s.cfg.UserData, ingest.RequestFiles[0])); err == nil {
			label += " (" + fi.ModTime().Format("Jan 2, 2006") + ")"
		}
		return s.cfg.UserData, label
	}
	return s.cfg.BundledData, "Bundled snapshot (Oct 9, 2026)"
}

type fileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type questionInfo struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Stakeholder string `json:"stakeholder"`
}

type datasetLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	dir, label := s.dataDir()
	var files []fileInfo
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			files = append(files, fileInfo{e.Name(), fi.Size()})
		}
	}
	var qs []questionInfo
	for _, a := range analysis.All() {
		qs = append(qs, questionInfo{a.Number(), a.Title(), a.Stakeholder()})
	}
	var links []datasetLink
	for _, l := range fetch.Layers {
		links = append(links, datasetLink{l.Name, l.ItemURL})
	}
	writeJSON(w, map[string]any{
		"cpus":       runtime.NumCPU(),
		"dataSource": label,
		"dataReady":  ingest.Available(dir),
		"files":      files,
		"questions":  qs,
		"datasets":   links,
	})
}

type runReq struct {
	Questions []int `json:"questions"`
	Workers   int   `json:"workers"`
}

type sectionOut struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	Stakeholder string   `json:"stakeholder"`
	Question    string   `json:"question"`
	ElapsedMs   float64  `json:"elapsedMs"`
	Findings    []string `json:"findings"`
	HTML        string   `json:"html"`
}

type stageOut struct {
	Name   string  `json:"name"`
	Ms     float64 `json:"ms"`
	HeapMB float64 `json:"heapMB"`
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req runReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	want := map[int]bool{}
	for _, q := range req.Questions {
		want[q] = true
	}
	var sel []analysis.Analysis
	for _, a := range analysis.All() {
		if want[a.Number()] {
			sel = append(sel, a)
		}
	}
	if len(sel) == 0 {
		fail(w, http.StatusBadRequest, fmt.Errorf("select at least one question"))
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()
	dir, label := s.dataDir()
	eng, err := engine.NewBuilder().DataDir(dir).Workers(req.Workers).Analyses(sel...).Build()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	res, err := eng.Run(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	s.lastM.Lock()
	s.last = res.Report
	s.lastM.Unlock()

	rep, m := res.Report, res.Metrics
	out := map[string]any{
		"subtitle":   rep.Subtitle,
		"dataSource": label,
		"workers":    m.Workers,
		"totalMs":    ms(m.Total),
		"records":    m.Records,
		"liveHeapMB": m.LiveHeapMB,
		"gcPauseMs":  ms(m.GCPause),
		"numGC":      m.NumGC,
		"preamble":   report.Fragment(rep.Preamble),
		"appendix":   report.Fragment(rep.Appendix),
	}
	var stages []stageOut
	for _, st := range m.Stages {
		stages = append(stages, stageOut{st.Name, ms(st.Dur), st.HeapMB})
	}
	out["stages"] = stages
	var secs []sectionOut
	for _, sec := range rep.Sections {
		so := sectionOut{Number: sec.Number, Title: sec.Title, Stakeholder: sec.Stakeholder,
			Question: sec.Question, ElapsedMs: ms(sec.Elapsed), HTML: report.Fragment(sec.Elements)}
		for _, el := range sec.Elements {
			if f, ok := el.(report.Finding); ok {
				so.Findings = append(so.Findings, f.Text)
			}
		}
		secs = append(secs, so)
	}
	out["sections"] = secs
	writeJSON(w, out)
}

// export writes the most recent report as a standalone HTML file to
// ~/Downloads and reveals it in Finder.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	s.lastM.Lock()
	rep := s.last
	s.lastM.Unlock()
	if rep == nil {
		fail(w, http.StatusConflict, fmt.Errorf("run an analysis first"))
		return
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Downloads")
	if _, err := os.Stat(dir); err != nil {
		dir = home
	}
	path := filepath.Join(dir, "Baltimore-Report-"+time.Now().Format("2006-01-02-150405")+".html")
	f, err := os.Create(path)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	err = (&report.HTMLRenderer{}).Render(f, rep)
	f.Close()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	reveal(path)
	writeJSON(w, map[string]string{"path": path})
}

func (s *Server) revealData(w http.ResponseWriter, r *http.Request) {
	dir, _ := s.dataDir()
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	exec.Command("open", dir).Start()
	writeJSON(w, map[string]string{"path": dir})
}

func reveal(path string) {
	if runtime.GOOS == "darwin" {
		exec.Command("open", "-R", path).Start()
	}
}

// ---- background data refresh ----------------------------------------------

func (s *Server) startFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	if s.fetching {
		fail(w, http.StatusConflict, fmt.Errorf("a download is already running"))
		return
	}
	if s.cfg.UserData == "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("no writable data folder configured"))
		return
	}
	s.fetching, s.fetchErr = true, ""
	s.fetchLog.reset()
	go func() {
		err := fetch.All(context.Background(), fetch.Options{OutDir: s.cfg.UserData, Workers: 6, Log: s.fetchLog})
		s.fetchMu.Lock()
		defer s.fetchMu.Unlock()
		s.fetching = false
		s.fetchEnd = time.Now()
		if err != nil {
			s.fetchErr = err.Error()
		}
	}()
	writeJSON(w, map[string]bool{"started": true})
}

func (s *Server) fetchStatus(w http.ResponseWriter, r *http.Request) {
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	writeJSON(w, map[string]any{
		"running": s.fetching,
		"error":   s.fetchErr,
		"lines":   s.fetchLog.lines(),
		"done":    !s.fetching && !s.fetchEnd.IsZero() && s.fetchErr == "",
	})
}

// lineLog is an io.Writer that keeps the last `max` lines, treating '\r' as
// "replace the current line" so carriage-return progress counters render as
// a single updating line.
type lineLog struct {
	mu   sync.Mutex
	max  int
	done []string
	cur  string
}

func (l *lineLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range string(p) {
		switch c {
		case '\n':
			l.done = append(l.done, l.cur)
			l.cur = ""
			if len(l.done) > l.max {
				l.done = l.done[len(l.done)-l.max:]
			}
		case '\r':
			l.cur = ""
		default:
			l.cur += string(c)
		}
	}
	return len(p), nil
}

func (l *lineLog) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]string(nil), l.done...)
	if strings.TrimSpace(l.cur) != "" {
		out = append(out, l.cur)
	}
	var trimmed []string
	for _, s := range out {
		if strings.TrimSpace(s) != "" {
			trimmed = append(trimmed, strings.TrimSpace(s))
		}
	}
	return trimmed
}

func (l *lineLog) reset() {
	l.mu.Lock()
	l.done, l.cur = nil, ""
	l.mu.Unlock()
}
