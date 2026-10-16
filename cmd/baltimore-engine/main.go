// Command baltimore-engine ingests Open Baltimore datasets, joins them, and
// answers five stakeholder research questions.
//
//	baltimore-engine [run] [flags]   analyse data in ./data and print the report
//	baltimore-engine fetch [flags]   (re)download raw datasets from Open Baltimore
//	baltimore-engine ui [flags]      interactive dashboard in the default browser
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"

	"github.com/aidan/baltimore-engine/internal/analysis"
	"github.com/aidan/baltimore-engine/internal/engine"
	"github.com/aidan/baltimore-engine/internal/fetch"
	"github.com/aidan/baltimore-engine/internal/report"
	"github.com/aidan/baltimore-engine/internal/ui"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = run(ctx, args)
	case "fetch":
		err = fetchCmd(ctx, args)
	case "ui":
		err = uiCmd(ctx, args)
	case "help", "-h", "--help":
		usage()
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `baltimore-engine — multi-dataset processing engine for Open Baltimore

Usage:
  baltimore-engine [run] [flags]     analyse ./data and print the report
  baltimore-engine fetch [flags]     download fresh raw data from data.baltimorecity.gov
  baltimore-engine ui [flags]        open the interactive dashboard in your browser

Run 'baltimore-engine run -h' or 'baltimore-engine fetch -h' for flags.
`)
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dataDir := fs.String("data", "data", "directory containing the raw dataset files")
	year := fs.Int("year", 2025, "analysis calendar year / liquor license year")
	workers := fs.Int("workers", 0, "parser/indexer goroutines per pool (0 = number of CPUs)")
	format := fs.String("format", "terminal", "stdout renderer: terminal | plain | html")
	htmlOut := fs.String("html", "", "also write an HTML report to this file")
	txtOut := fs.String("out", "", "also write a plain-text report to this file")
	gc := fs.Int("gc", -2, "GOGC percent for the run (e.g. 200); default leaves the runtime setting")
	only := fs.String("only", "", "comma-separated question numbers to run, e.g. 1,3")
	quiet := fs.Bool("q", false, "suppress stage timing on stderr")
	fs.Parse(args)

	r, err := report.NewRenderer(*format)
	if err != nil {
		return err
	}
	b := engine.NewBuilder().DataDir(*dataDir).Year(*year).Workers(*workers).GCPercent(*gc).Output(r, os.Stdout)
	if !*quiet {
		b.Log(os.Stderr)
	}
	if *only != "" {
		want := map[int]bool{}
		for _, s := range strings.Split(*only, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				return fmt.Errorf("-only: %w", err)
			}
			want[n] = true
		}
		var sel []analysis.Analysis
		for _, a := range analysis.All() {
			if want[a.Number()] {
				sel = append(sel, a)
			}
		}
		b.Analyses(sel...)
	}
	for _, o := range []struct{ path, kind string }{{*htmlOut, "html"}, {*txtOut, "plain"}} {
		if o.path == "" {
			continue
		}
		f, err := os.Create(o.path)
		if err != nil {
			return err
		}
		defer f.Close()
		rr, _ := report.NewRenderer(o.kind)
		b.Output(rr, f)
	}
	eng, err := b.Build()
	if err != nil {
		return err
	}
	if _, err := eng.Run(ctx); err != nil {
		return err
	}
	if *htmlOut != "" && !*quiet {
		fmt.Fprintf(os.Stderr, "HTML report written to %s  (open it with: open %s)\n", *htmlOut, *htmlOut)
	}
	return nil
}

func fetchCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	dataDir := fs.String("data", "data", "output directory")
	workers := fs.Int("workers", 6, "concurrent HTTP page requests per dataset")
	fs.Parse(args)
	return fetch.All(ctx, fetch.Options{OutDir: *dataDir, Workers: *workers, Log: os.Stderr})
}

func uiCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	dataDir := fs.String("data", "data", "directory containing the bundled dataset files")
	noOpen := fs.Bool("no-open", false, "print the URL instead of opening a browser")
	fs.Parse(args)
	srv := ui.New(ui.Config{BundledData: *dataDir, UserData: ui.DefaultUserData()})
	url, err := srv.Start()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Dashboard running at %s\nPress Ctrl-C to stop.\n", url)
	if !*noOpen {
		exec.Command("open", url).Start()
	}
	<-ctx.Done()
	return nil
}
