package normalize

import (
	"sync"
	"testing"
	"time"
)

func TestBlockKey(t *testing.T) {
	cases := map[string]string{
		"4200 NICHOLAS AVE":                       "4200 NICHOLAS AVE",
		"1713 N CHAPEL ST, Baltimore City, 21213": "1700 N CHAPEL ST",
		"2113 WILKENS AVE":                        "2100 WILKENS AVE",
		"6412-16 FRANKFORD AVENUE":                "6400 FRANKFORD AVE",
		"3208 Greenmount Avenue":                  "3200 GREENMOUNT AVE",
		"55 West Street":                          "0 W ST", // direction-only street name keeps suffix logic sane
		"1000 NORTH CHARLES STREET APT 4":         "1000 N CHARLES ST",
		"712A W NORTH AVENUE":                     "700 W NORTH AVE",
		"CITYWIDE":                                "",
		"":                                        "",
		"N CHARLES ST & E 25TH ST":                "",
	}
	for in, want := range cases {
		if got := BlockKey(in); got != want {
			t.Errorf("BlockKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlockKeyCrossDatasetAgreement(t *testing.T) {
	// The same hundred-block as spelled by four different city systems.
	forms := []string{
		"3200 GREENMOUNT AVE",                        // BPD crime
		"3208 GREENMOUNT AVE, Baltimore City, 21218", // 311
		"3215 GREENMOUNT AVE",                        // DHCD vacants
		"3208-10 GREENMOUNT AVENUE",                  // Liquor Board
	}
	want := BlockKey(forms[0])
	for _, f := range forms[1:] {
		if got := BlockKey(f); got != want {
			t.Errorf("BlockKey(%q) = %q, want %q", f, got, want)
		}
	}
}

func TestNeighborhoodKey(t *testing.T) {
	if got := NeighborhoodKey("  Broadway   East "); got != "BROADWAY EAST" {
		t.Fatalf("got %q", got)
	}
}

func TestParseTime(t *testing.T) {
	want := time.Date(2025, 4, 28, 16, 31, 3, 0, time.UTC).Unix()
	for _, s := range []string{"2025/04/28 16:31:03+00", "2025-04-28T16:31:03Z", "1745857863000"} {
		if got := ParseTime(s); got != want {
			t.Errorf("ParseTime(%q) = %d, want %d", s, got, want)
		}
	}
	if ParseTime("garbage") != 0 || ParseTime("") != 0 {
		t.Error("expected 0 for unparseable input")
	}
}

func TestInternerConcurrent(t *testing.T) {
	in := NewInterner()
	var wg sync.WaitGroup
	results := make([]string, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = in.Intern(string([]byte("SANDTOWN-WINCHESTER")))
		}(i)
	}
	wg.Wait()
	if in.Len() != 1 {
		t.Fatalf("expected 1 unique string, got %d", in.Len())
	}
	for _, r := range results[1:] {
		if r != results[0] {
			t.Fatal("interned values differ")
		}
	}
}

func BenchmarkBlockKey(b *testing.B) {
	for i := 0; i < b.N; i++ {
		BlockKey("1713 N CHAPEL ST, Baltimore City, 21213")
	}
}
