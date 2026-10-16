package report

import (
	"bufio"
	"fmt"
	"html"
	"io"
	"math"
	"strings"
)

// HTMLRenderer writes a single self-contained HTML page (no external assets)
// that can be opened directly in Safari/Chrome: `open report.html`.
type HTMLRenderer struct{}

const css = `
:root{--bg:#f7f7f5;--fg:#1d1d1f;--muted:#6b6b70;--card:#fff;--line:#e3e3e0;--a:#2a6fdb;--b:#e08a1e;--ok:#1d7a46}
@media (prefers-color-scheme:dark){:root{--bg:#141416;--fg:#ececef;--muted:#9a9aa2;--card:#1d1d21;--line:#2e2e34;--a:#6ea2ff;--b:#f0a94a;--ok:#5fd394}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
main{max-width:1000px;margin:0 auto;padding:24px 16px 64px}
h1{font-size:26px;margin:8px 0 2px}.sub{color:var(--muted);margin:0 0 20px}
section{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:18px 20px;margin:18px 0}
h2{font-size:19px;margin:0 0 4px}.meta{color:var(--muted);font-size:13px;margin-bottom:10px}
.q{font-weight:600;margin:8px 0 12px}
.finding{border-left:3px solid var(--ok);padding:6px 12px;margin:12px 0;background:color-mix(in srgb,var(--ok) 8%,transparent)}
h3{font-size:14px;margin:18px 0 6px;color:var(--muted);text-transform:uppercase;letter-spacing:.04em}
.tw{overflow-x:auto}table{border-collapse:collapse;width:100%;font-size:13.5px;font-variant-numeric:tabular-nums}
th,td{padding:5px 8px;border-bottom:1px solid var(--line);text-align:left;white-space:nowrap}th{font-weight:600}td.n,th.n{text-align:right}
.bar{display:grid;grid-template-columns:minmax(120px,240px) 1fr 80px;gap:8px;align-items:center;font-size:13.5px;margin:3px 0}
.bar .l{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.bar .t{background:var(--line);border-radius:3px;height:14px}
.bar .f{background:var(--a);height:14px;border-radius:3px}.bar .v{text-align:right;font-variant-numeric:tabular-nums}
.hist{display:flex;align-items:flex-end;gap:3px;height:160px;border-bottom:1px solid var(--line);padding-top:8px}
.hist .c{flex:1;display:flex;align-items:flex-end;gap:1px;height:100%}.hist .a{background:var(--b);flex:1}.hist .b{background:var(--a);flex:1}
.hl{display:flex;gap:3px;font-size:11px;color:var(--muted)}.hl span{flex:1;min-width:0;text-align:center}
.legend{font-size:13px;color:var(--muted);margin-top:6px}.sw{display:inline-block;width:10px;height:10px;margin:0 4px 0 12px;border-radius:2px}
`

func (h *HTMLRenderer) Render(out io.Writer, r *Report) error {
	w := bufio.NewWriter(out)
	e := html.EscapeString
	fmt.Fprintf(w, "<!doctype html><html lang=en><head><meta charset=utf-8><meta name=viewport content=\"width=device-width,initial-scale=1\"><title>%s</title><style>%s</style></head><body><main>", e(r.Title), css)
	fmt.Fprintf(w, "<h1>%s</h1><p class=sub>%s</p>", e(r.Title), e(r.Subtitle))
	if len(r.Preamble) > 0 {
		fmt.Fprint(w, "<section><h2>Data provenance &amp; ingestion</h2>")
		for _, el := range r.Preamble {
			h.element(w, el)
		}
		fmt.Fprint(w, "</section>")
	}
	for _, s := range r.Sections {
		fmt.Fprintf(w, "<section><h2>Q%d. %s</h2><div class=meta>Stakeholder: %s · computed in %s</div><div class=q>%s</div>",
			s.Number, e(s.Title), e(s.Stakeholder), s.Elapsed, e(s.Question))
		for _, el := range s.Elements {
			h.element(w, el)
		}
		fmt.Fprint(w, "</section>")
	}
	if len(r.Appendix) > 0 {
		fmt.Fprint(w, "<section><h2>Engine runtime &amp; memory profile</h2>")
		for _, el := range r.Appendix {
			h.element(w, el)
		}
		fmt.Fprint(w, "</section>")
	}
	fmt.Fprint(w, "</main></body></html>\n")
	return w.Flush()
}

// CSS returns the stylesheet used by HTML output, for embedding elsewhere.
func CSS() string { return css }

// Fragment renders elements to an HTML snippet (no <html>/<body>), so a UI
// can drop report sections into its own page.
func Fragment(els []Element) string {
	var b strings.Builder
	h := &HTMLRenderer{}
	for _, el := range els {
		h.element(&b, el)
	}
	return b.String()
}

func (h *HTMLRenderer) element(w io.Writer, el Element) {
	e := html.EscapeString
	switch el := el.(type) {
	case Para:
		fmt.Fprintf(w, "<p>%s</p>", e(el.Text))
	case Finding:
		fmt.Fprintf(w, "<div class=finding>%s</div>", e(el.Text))
	case Table:
		if el.Title != "" {
			fmt.Fprintf(w, "<h3>%s</h3>", e(el.Title))
		}
		fmt.Fprint(w, "<div class=tw><table><tr>")
		for i, hd := range el.Headers {
			fmt.Fprintf(w, "<th%s>%s</th>", numCls(el.NumericCols[i]), e(hd))
		}
		fmt.Fprint(w, "</tr>")
		for _, row := range el.Rows {
			fmt.Fprint(w, "<tr>")
			for i, c := range row {
				fmt.Fprintf(w, "<td%s>%s</td>", numCls(el.NumericCols[i]), e(c))
			}
			fmt.Fprint(w, "</tr>")
		}
		fmt.Fprint(w, "</table></div>")
	case Bars:
		if el.Title != "" {
			fmt.Fprintf(w, "<h3>%s</h3>", e(el.Title))
		}
		maxV := 0.0
		for _, v := range el.Values {
			maxV = math.Max(maxV, v)
		}
		format := el.Format
		if format == "" {
			format = "%.1f"
		}
		for i, l := range el.Labels {
			pct := 0.0
			if maxV > 0 {
				pct = el.Values[i] / maxV * 100
			}
			fmt.Fprintf(w, "<div class=bar><span class=l title=\"%s\">%s</span><span class=t><div class=f style=\"width:%.1f%%\"></div></span><span class=v>%s</span></div>",
				e(l), e(l), pct, e(fmt.Sprintf(format, el.Values[i])+el.Unit))
		}
	case Histogram:
		if el.Title != "" {
			fmt.Fprintf(w, "<h3>%s</h3>", e(el.Title))
		}
		maxV := 0.0
		for i := range el.A {
			maxV = math.Max(maxV, math.Max(el.A[i], el.B[i]))
		}
		fmt.Fprint(w, "<div class=hist>")
		for i := range el.A {
			fmt.Fprintf(w, "<div class=c title=\"%s: %.1f%% / %.1f%%\"><div class=a style=\"height:%.1f%%\"></div><div class=b style=\"height:%.1f%%\"></div></div>",
				e(el.Buckets[i]), el.A[i], el.B[i], el.A[i]/maxV*100, el.B[i]/maxV*100)
		}
		fmt.Fprint(w, "</div><div class=hl>")
		for i, b := range el.Buckets {
			if len(el.Buckets) > 12 && i%3 != 0 {
				b = "" // label every 3rd bucket so 24 hours fit on a phone
			}
			fmt.Fprintf(w, "<span>%s</span>", e(b))
		}
		fmt.Fprintf(w, "</div><div class=legend><span class=sw style=\"background:var(--b)\"></span>%s<span class=sw style=\"background:var(--a)\"></span>%s</div>", e(el.NameA), e(el.NameB))
	}
}

func numCls(n bool) string {
	if n {
		return " class=n"
	}
	return ""
}
