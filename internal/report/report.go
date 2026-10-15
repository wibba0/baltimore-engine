// Package report defines a format-neutral report model and a set of
// Renderer strategies (ANSI terminal, plain ASCII, HTML) that format it.
// Analyses build Sections out of Elements and never print directly, which
// keeps cross-dataset logic completely separate from presentation.
package report

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode"
)

// Element is one renderable block inside a Section.
type Element interface{ element() }

// Para is a paragraph of explanatory text.
type Para struct{ Text string }

// Finding is a highlighted, decision-ready takeaway.
type Finding struct{ Text string }

// Table is a simple grid. Columns whose header begins with a space or that
// are listed in NumericCols are right-aligned.
type Table struct {
	Title       string
	Headers     []string
	Rows        [][]string
	NumericCols map[int]bool
}

// Bars is a horizontal bar chart.
type Bars struct {
	Title  string
	Labels []string
	Values []float64
	Format string // fmt verb for values, e.g. "%.1f" or "%.0f"
	Unit   string
}

// Histogram is a vertical, two-series comparison over ordered buckets
// (e.g. hour of day), with values expressed as percentages of each series.
type Histogram struct {
	Title        string
	Buckets      []string
	A, B         []float64
	NameA, NameB string
}

func (Para) element()      {}
func (Finding) element()   {}
func (Table) element()     {}
func (Bars) element()      {}
func (Histogram) element() {}

// Section answers one research question.
type Section struct {
	Number      int
	Title       string
	Stakeholder string
	Question    string
	Elements    []Element
	Elapsed     time.Duration
}

// Report is the complete output document.
type Report struct {
	Title    string
	Subtitle string
	Preamble []Element // data provenance, ingestion stats
	Sections []Section
	Appendix []Element // runtime / memory metrics
}

// Renderer is the output Strategy.
type Renderer interface {
	Render(w io.Writer, r *Report) error
}

// NewRenderer is a Factory for renderers by name.
func NewRenderer(name string) (Renderer, error) {
	switch name {
	case "terminal", "term", "ansi":
		return &TextRenderer{Color: true, Unicode: true, Width: 100}, nil
	case "plain", "text", "ascii":
		return &TextRenderer{Color: false, Unicode: false, Width: 100}, nil
	case "html":
		return &HTMLRenderer{}, nil
	}
	return nil, fmt.Errorf("unknown renderer %q (want terminal|plain|html)", name)
}

// ---- formatting helpers shared by analyses --------------------------------

// Int formats an integer with thousands separators.
func Int(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func F1(f float64) string { return fmt.Sprintf("%.1f", f) }
func F2(f float64) string { return fmt.Sprintf("%.2f", f) }
func Pct(f float64) string {
	if math.IsNaN(f) {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", f*100)
}

// Name title-cases an upper-case neighborhood key for display
// ("PENROSE/FAYETTE STREET OUTREACH" → "Penrose/Fayette Street Outreach").
func Name(s string) string {
	out := []rune(strings.ToLower(s))
	for i := range out {
		if i == 0 || strings.ContainsRune(" -/'(.", out[i-1]) {
			out[i] = unicode.ToUpper(out[i])
		}
	}
	return string(out)
}
