// Package normalize turns the inconsistent text found across Open Baltimore
// datasets into canonical join keys.
//
// The four source datasets were produced by four different city systems
// (BPD records, the CSR/311 CRM, DHCD code enforcement and the Liquor Board),
// so the same place is spelled differently in each:
//
//	crime   : "4200 NICHOLAS AVE"                (already block-level)
//	311     : "1713 N CHAPEL ST, Baltimore City, 21213"
//	vacants : "2113 WILKENS AVE"
//	liquor  : "6412-16 FRANKFORD AVENUE"
//
// BlockKey reduces all of them to "<hundred-block> <DIR> <NAME> <SUFFIX>",
// e.g. "1700 N CHAPEL ST", which is the cross-dataset match key.
package normalize

import (
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

var suffixes = map[string]string{
	"AVENUE": "AVE", "AV": "AVE", "AVE": "AVE",
	"STREET": "ST", "STR": "ST", "ST": "ST",
	"ROAD": "RD", "RD": "RD",
	"BOULEVARD": "BLVD", "BLVD": "BLVD",
	"DRIVE": "DR", "DR": "DR",
	"LANE": "LN", "LN": "LN",
	"PLACE": "PL", "PL": "PL",
	"COURT": "CT", "CT": "CT",
	"TERRACE": "TER", "TERR": "TER", "TER": "TER",
	"PARKWAY": "PKWY", "PKWY": "PKWY",
	"HIGHWAY": "HWY", "HWY": "HWY",
	"CIRCLE": "CIR", "CIR": "CIR",
	"SQUARE": "SQ", "SQ": "SQ",
	"ALLEY": "ALY", "ALY": "ALY",
	"WAY": "WAY", "PIKE": "PIKE", "ROW": "ROW", "WALK": "WALK",
}

var directions = map[string]string{
	"NORTH": "N", "N": "N", "SOUTH": "S", "S": "S",
	"EAST": "E", "E": "E", "WEST": "W", "W": "W",
}

// BlockKey converts a street address into a hundred-block key. It returns ""
// when the address has no leading house number (intersections, PO boxes,
// "CITYWIDE", etc.), which callers treat as "unmatchable".
//
// The function is allocation-light: it tokenises with strings.Fields on an
// upper-cased copy and builds the result with a single strings.Builder.
func BlockKey(addr string) string {
	if addr == "" {
		return ""
	}
	// 311 addresses carry ", Baltimore City, 21213"; keep only the street part.
	if i := strings.IndexByte(addr, ','); i >= 0 {
		addr = addr[:i]
	}
	toks := strings.Fields(strings.ToUpper(addr))
	if len(toks) < 2 {
		return ""
	}
	num := leadingNumber(toks[0])
	if num < 0 {
		return ""
	}
	toks = toks[1:]
	// Drop unit designators and everything after them.
	for i, t := range toks {
		if t == "#" || strings.HasPrefix(t, "#") || t == "APT" || t == "UNIT" || t == "STE" || t == "REAR" {
			toks = toks[:i]
			break
		}
	}
	if len(toks) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(addr) + 4)
	b.WriteString(strconv.Itoa(num / 100 * 100))
	for i, t := range toks {
		t = strings.TrimFunc(t, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if t == "" {
			continue
		}
		if d, ok := directions[t]; ok && i == 0 && len(toks) > 1 {
			t = d
		} else if s, ok := suffixes[t]; ok && i == len(toks)-1 && i > 0 {
			t = s
		}
		b.WriteByte(' ')
		b.WriteString(t)
	}
	return b.String()
}

// leadingNumber parses "6412", "6412-16" or "6412A" → 6412; -1 if not numeric.
func leadingNumber(tok string) int {
	end := 0
	for end < len(tok) && tok[end] >= '0' && tok[end] <= '9' {
		end++
	}
	if end == 0 {
		return -1
	}
	n, err := strconv.Atoi(tok[:end])
	if err != nil {
		return -1
	}
	return n
}

// NeighborhoodKey canonicalises neighborhood names ("Broadway East",
// "BROADWAY EAST", "Broadway  East ") to one upper-case, single-spaced key.
func NeighborhoodKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(strings.ToUpper(s)), " ")
}

var timeLayouts = []string{
	"2006/01/02 15:04:05+00",
	"2006/01/02 15:04:05",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"01/02/2006 15:04:05",
	"2006-01-02",
}

// ParseTime parses the date encodings found in Open Baltimore exports (and
// raw epoch-milliseconds as found in GeoJSON). Returns Unix seconds, or 0.
func ParseTime(s string) int64 {
	if s == "" {
		return 0
	}
	if s[0] >= '0' && s[0] <= '9' && !strings.ContainsAny(s, "/-: ") {
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			return ms / 1000
		}
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// Interner deduplicates repeated strings (neighborhood names, crime
// descriptions, 311 types). ~1M 311 rows contain only ~280 neighborhoods and
// ~400 request types; interning lets every record point at one shared
// backing array instead of retaining a substring of its whole CSV line, which
// both shrinks the live heap and lets the GC free the raw line buffers.
//
// It is safe for concurrent use; the read path takes only an RLock.
type Interner struct {
	mu sync.RWMutex
	m  map[string]string
}

func NewInterner() *Interner { return &Interner{m: make(map[string]string, 4096)} }

func (in *Interner) Intern(s string) string {
	if s == "" {
		return ""
	}
	in.mu.RLock()
	v, ok := in.m[s]
	in.mu.RUnlock()
	if ok {
		return v
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if v, ok := in.m[s]; ok {
		return v
	}
	c := strings.Clone(s) // detach from the CSV line buffer
	in.m[c] = c
	return c
}

func (in *Interner) Len() int {
	in.mu.RLock()
	defer in.mu.RUnlock()
	return len(in.m)
}
