package report

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

// TextRenderer draws the report for a terminal. With Color it emits ANSI
// escapes; with Unicode it uses block glyphs for sub-character bar precision,
// otherwise plain '#' bars so the output is safe to paste anywhere.
type TextRenderer struct {
	Color   bool
	Unicode bool
	Width   int
}

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiCyan   = "\033[36m"
	ansiYellow = "\033[33m"
	ansiGreen  = "\033[32m"
	ansiMag    = "\033[35m"
	ansiBlue   = "\033[34m"
)

func (t *TextRenderer) c(code, s string) string {
	if !t.Color {
		return s
	}
	return code + s + ansiReset
}

func (t *TextRenderer) Render(out io.Writer, r *Report) error {
	w := bufio.NewWriter(out)
	rule := strings.Repeat(t.pick("═", "="), t.Width)
	thin := strings.Repeat(t.pick("─", "-"), t.Width)

	fmt.Fprintln(w, t.c(ansiCyan, rule))
	fmt.Fprintln(w, t.c(ansiBold, center(r.Title, t.Width)))
	fmt.Fprintln(w, t.c(ansiDim, center(r.Subtitle, t.Width)))
	fmt.Fprintln(w, t.c(ansiCyan, rule))
	for _, e := range r.Preamble {
		t.element(w, e)
	}
	for _, s := range r.Sections {
		fmt.Fprintln(w)
		fmt.Fprintln(w, t.c(ansiCyan, rule))
		fmt.Fprintln(w, t.c(ansiBold+ansiYellow, fmt.Sprintf(" Q%d. %s", s.Number, s.Title)))
		fmt.Fprintln(w, t.c(ansiDim, fmt.Sprintf(" Stakeholder: %s   (computed in %s)", s.Stakeholder, s.Elapsed)))
		fmt.Fprintln(w, t.c(ansiCyan, thin))
		for _, line := range wrap(s.Question, t.Width-4) {
			fmt.Fprintln(w, "  "+t.c(ansiBold, line))
		}
		for _, e := range s.Elements {
			t.element(w, e)
		}
	}
	if len(r.Appendix) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, t.c(ansiCyan, rule))
		fmt.Fprintln(w, t.c(ansiBold, " Engine runtime & memory profile"))
		fmt.Fprintln(w, t.c(ansiCyan, thin))
		for _, e := range r.Appendix {
			t.element(w, e)
		}
	}
	fmt.Fprintln(w, t.c(ansiCyan, rule))
	return w.Flush()
}

func (t *TextRenderer) pick(u, a string) string {
	if t.Unicode {
		return u
	}
	return a
}

func (t *TextRenderer) element(w io.Writer, e Element) {
	switch e := e.(type) {
	case Para:
		fmt.Fprintln(w)
		for _, line := range wrap(e.Text, t.Width-4) {
			fmt.Fprintln(w, "  "+line)
		}
	case Finding:
		fmt.Fprintln(w)
		lines := wrap(e.Text, t.Width-8)
		for i, line := range lines {
			prefix := "    "
			if i == 0 {
				prefix = t.pick("  ▶ ", "  > ")
			}
			fmt.Fprintln(w, t.c(ansiGreen, prefix+line))
		}
	case Table:
		t.table(w, e)
	case Bars:
		t.bars(w, e)
	case Histogram:
		t.hist(w, e)
	}
}

func (t *TextRenderer) table(w io.Writer, tb Table) {
	fmt.Fprintln(w)
	if tb.Title != "" {
		fmt.Fprintln(w, "  "+t.c(ansiBold+ansiMag, tb.Title))
	}
	widths := make([]int, len(tb.Headers))
	for i, h := range tb.Headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range tb.Rows {
		for i, c := range row {
			if i < len(widths) && utf8.RuneCountInString(c) > widths[i] {
				widths[i] = utf8.RuneCountInString(c)
			}
		}
	}
	cell := func(i int, s string) string {
		if tb.NumericCols[i] {
			return pad(s, widths[i], true)
		}
		return pad(s, widths[i], false)
	}
	var hdr []string
	for i, h := range tb.Headers {
		hdr = append(hdr, cell(i, h))
	}
	fmt.Fprintln(w, "  "+t.c(ansiBold, strings.Join(hdr, "  ")))
	var seps []string
	for _, wd := range widths {
		seps = append(seps, strings.Repeat(t.pick("─", "-"), wd))
	}
	fmt.Fprintln(w, "  "+t.c(ansiDim, strings.Join(seps, "  ")))
	for _, row := range tb.Rows {
		var cs []string
		for i := range tb.Headers {
			v := ""
			if i < len(row) {
				v = row[i]
			}
			cs = append(cs, cell(i, v))
		}
		fmt.Fprintln(w, "  "+strings.Join(cs, "  "))
	}
}

// bar returns a bar of length frac*width using eighth-blocks when Unicode.
func (t *TextRenderer) bar(frac float64, width int) string {
	if frac < 0 || math.IsNaN(frac) {
		frac = 0
	}
	if !t.Unicode {
		return strings.Repeat("#", int(math.Round(frac*float64(width))))
	}
	eighths := int(math.Round(frac * float64(width) * 8))
	full, rem := eighths/8, eighths%8
	s := strings.Repeat("█", full)
	if rem > 0 {
		s += string([]rune(" ▏▎▍▌▋▊▉")[rem])
	}
	return s
}

func (t *TextRenderer) bars(w io.Writer, b Bars) {
	fmt.Fprintln(w)
	if b.Title != "" {
		fmt.Fprintln(w, "  "+t.c(ansiBold+ansiMag, b.Title))
	}
	lw := 0
	for _, l := range b.Labels {
		lw = max(lw, utf8.RuneCountInString(l))
	}
	lw = min(lw, 34)
	maxV := 0.0
	for _, v := range b.Values {
		maxV = math.Max(maxV, v)
	}
	format := b.Format
	if format == "" {
		format = "%.1f"
	}
	barW := t.Width - lw - 20
	for i, l := range b.Labels {
		frac := 0.0
		if maxV > 0 {
			frac = b.Values[i] / maxV
		}
		val := fmt.Sprintf(format, b.Values[i]) + b.Unit
		fmt.Fprintf(w, "  %s %s %s\n", pad(truncate(l, lw), lw, false), t.c(ansiBlue, t.bar(frac, barW)), val)
	}
}

func (t *TextRenderer) hist(w io.Writer, h Histogram) {
	fmt.Fprintln(w)
	if h.Title != "" {
		fmt.Fprintln(w, "  "+t.c(ansiBold+ansiMag, h.Title))
	}
	const height = 10
	maxV := 0.0
	for i := range h.A {
		maxV = math.Max(maxV, math.Max(h.A[i], h.B[i]))
	}
	if maxV == 0 {
		return
	}
	full, half := t.pick("█", "#"), t.pick("▒", ":")
	for row := height; row >= 1; row-- {
		thr := maxV * float64(row) / height
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("  %5.1f%% │", thr))
		if !t.Unicode {
			sb.Reset()
			sb.WriteString(fmt.Sprintf("  %5.1f%% |", thr))
		}
		for i := range h.A {
			a, b := " ", " "
			if h.A[i] >= thr-maxV/(2*height) {
				a = t.c(ansiYellow, full)
			}
			if h.B[i] >= thr-maxV/(2*height) {
				b = t.c(ansiBlue, half)
			}
			sb.WriteString(a + b + " ")
		}
		fmt.Fprintln(w, sb.String())
	}
	axis := "         " + t.pick("└", "+") + strings.Repeat(t.pick("─", "-"), len(h.Buckets)*3)
	fmt.Fprintln(w, axis)
	var lb strings.Builder
	lb.WriteString("          ")
	for _, b := range h.Buckets {
		lb.WriteString(pad(b, 3, false))
	}
	fmt.Fprintln(w, lb.String())
	fmt.Fprintf(w, "          %s %s    %s %s\n", t.c(ansiYellow, full), h.NameA, t.c(ansiBlue, half), h.NameB)
}

// ---- text utilities --------------------------------------------------------

func pad(s string, n int, right bool) string {
	l := utf8.RuneCountInString(s)
	if l >= n {
		return s
	}
	if right {
		return strings.Repeat(" ", n-l) + s
	}
	return s + strings.Repeat(" ", n-l)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func center(s string, w int) string {
	l := utf8.RuneCountInString(s)
	if l >= w {
		return s
	}
	return strings.Repeat(" ", (w-l)/2) + s
}

func wrap(s string, w int) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		line := ""
		for _, word := range words {
			if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > w {
				lines = append(lines, line)
				line = word
			} else if line == "" {
				line = word
			} else {
				line += " " + word
			}
		}
		lines = append(lines, line)
	}
	return lines
}
