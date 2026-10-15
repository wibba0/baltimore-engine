// Package index joins the four datasets on two shared keys — normalised
// neighborhood name and hundred-block address — producing per-key aggregates
// that every analysis reads.
//
// Construction is a lock-free sharded map-reduce: each dataset slice is cut
// into W contiguous shards, W goroutines each build a *private* partial Index
// (no shared writes ⇒ no mutexes, no false sharing on counters), and the
// partials are merged on one goroutine at the end. Merging ~300 neighborhoods
// and ~25k blocks W times is far cheaper than contending on a shared map for
// ~1M inserts.
package index

import (
	"strings"
	"sync"

	"github.com/aidan/baltimore-engine/internal/model"
)

// Neighborhood aggregates every dataset for one neighborhood.
type Neighborhood struct {
	Name                           string
	Crimes, Violent, Property      int
	Shootings                      int
	Vacants                        int
	Liquor                         int
	Requests, FieldRequests        int
	Blight                         int
	FieldClosed, PastDue, DueKnown int
	CloseDays                      []float32 // per closed field request, for medians
	CouncilVotes                   [15]int   // 311 + vacant records vote for council district
}

// Council returns the council district most records in this neighborhood
// report (neighborhoods occasionally straddle a boundary).
func (n *Neighborhood) Council() uint8 {
	best, bestN := 0, 0
	for d, c := range n.CouncilVotes {
		if c > bestN {
			best, bestN = d, c
		}
	}
	return uint8(best)
}

// Block aggregates every dataset for one hundred-block.
type Block struct {
	Key               string
	Neighborhood      string
	Crimes, Violent   int
	Vacants, Liquor   int
	Requests, Blight  int
	NeighborhoodVotes map[string]int
}

// Index is the joined view.
type Index struct {
	Neighborhoods map[string]*Neighborhood
	Blocks        map[string]*Block
	AsOf          int64 // reference time for "still open and overdue"
}

func newIndex(asOf int64) *Index {
	return &Index{Neighborhoods: make(map[string]*Neighborhood, 300), Blocks: make(map[string]*Block, 30000), AsOf: asOf}
}

func (ix *Index) nb(name string) *Neighborhood {
	n := ix.Neighborhoods[name]
	if n == nil {
		n = &Neighborhood{Name: name}
		ix.Neighborhoods[name] = n
	}
	return n
}

func (ix *Index) blk(key string) *Block {
	b := ix.Blocks[key]
	if b == nil {
		b = &Block{Key: key}
		ix.Blocks[key] = b
	}
	return b
}

func (b *Block) vote(nbhd string) {
	if nbhd == "" {
		return
	}
	if b.NeighborhoodVotes == nil {
		b.NeighborhoodVotes = make(map[string]int, 1)
	}
	b.NeighborhoodVotes[nbhd]++
}

// ---- 311 request classification (Strategy-style predicates) ---------------

// IsFieldService reports whether a 311 type represents real field work that
// an agency must dispatch and close (as opposed to call-center information
// requests, city-initiated "Proactive" sweeps, or BGE-owned streetlights).
func IsFieldService(t string) bool {
	return t != "" && !strings.HasPrefix(t, "ECC-") && !strings.HasPrefix(t, "BGE-") &&
		!strings.Contains(t, "Proactive")
}

// blightTypes are resident-reported sanitation / nuisance-property requests.
var blightTypes = map[string]bool{
	"HCD-Sanitation Property":        true,
	"HCD-Illegal Dumping":            true,
	"SW-Dirty Alley":                 true,
	"SW-Dirty Street":                true,
	"SW-Mixed Refuse":                true,
	"SW-Rat Rubout":                  true,
	"SW-Boarding":                    true,
	"SW-Cleaning":                    true,
	"HCD-Maintenance Structure":      true,
	"HCD-Vacant Building":            true,
	"SW-SIU Clean Up (HCD USE ONLY)": false, // internal referral, not resident-reported
}

func IsBlight(t string) bool { return blightTypes[t] }

// ---- Build ----------------------------------------------------------------

const secsPerDay = 86400

func (ix *Index) addCrimes(cs []model.Crime) {
	for i := range cs {
		c := &cs[i]
		violent := c.Category == model.CatViolent
		if c.Neighborhood != "" {
			n := ix.nb(c.Neighborhood)
			n.Crimes++
			if violent {
				n.Violent++
			}
			if c.Category == model.CatProperty {
				n.Property++
			}
			if c.Shooting {
				n.Shootings++
			}
		}
		if c.Block != "" {
			b := ix.blk(c.Block)
			b.Crimes++
			if violent {
				b.Violent++
			}
			b.vote(c.Neighborhood)
		}
	}
}

func (ix *Index) addRequests(rs []model.ServiceRequest) {
	for i := range rs {
		r := &rs[i]
		field := IsFieldService(r.Type)
		blight := IsBlight(r.Type)
		if r.Neighborhood != "" {
			n := ix.nb(r.Neighborhood)
			n.Requests++
			n.CouncilVotes[r.Council]++
			if blight {
				n.Blight++
			}
			if field {
				n.FieldRequests++
				if r.IsClosed() {
					n.FieldClosed++
					n.CloseDays = append(n.CloseDays, float32(r.Closed-r.Created)/secsPerDay)
				}
				if r.Due > 0 {
					n.DueKnown++
					if (r.IsClosed() && r.Closed > r.Due) || (!r.IsClosed() && r.Due < ix.AsOf) {
						n.PastDue++
					}
				}
			}
		}
		if r.Block != "" {
			b := ix.blk(r.Block)
			b.Requests++
			if blight {
				b.Blight++
			}
			b.vote(r.Neighborhood)
		}
	}
}

func (ix *Index) addVacants(vs []model.VacantNotice) {
	for i := range vs {
		v := &vs[i]
		if v.Neighborhood != "" {
			n := ix.nb(v.Neighborhood)
			n.Vacants++
			n.CouncilVotes[v.Council]++
		}
		if v.Block != "" {
			b := ix.blk(v.Block)
			b.Vacants++
			b.vote(v.Neighborhood)
		}
	}
}

func (ix *Index) addLiquor(ls []model.LiquorLicense) {
	for i := range ls {
		if ls[i].Block != "" {
			ix.blk(ls[i].Block).Liquor++
		}
	}
}

// merge folds src into ix. Called only on the reducing goroutine.
func (ix *Index) merge(src *Index) {
	for k, s := range src.Neighborhoods {
		d := ix.nb(k)
		d.Crimes += s.Crimes
		d.Violent += s.Violent
		d.Property += s.Property
		d.Shootings += s.Shootings
		d.Vacants += s.Vacants
		d.Liquor += s.Liquor
		d.Requests += s.Requests
		d.FieldRequests += s.FieldRequests
		d.Blight += s.Blight
		d.FieldClosed += s.FieldClosed
		d.PastDue += s.PastDue
		d.DueKnown += s.DueKnown
		d.CloseDays = append(d.CloseDays, s.CloseDays...)
		for i := range d.CouncilVotes {
			d.CouncilVotes[i] += s.CouncilVotes[i]
		}
	}
	for k, s := range src.Blocks {
		d := ix.blk(k)
		d.Crimes += s.Crimes
		d.Violent += s.Violent
		d.Vacants += s.Vacants
		d.Liquor += s.Liquor
		d.Requests += s.Requests
		d.Blight += s.Blight
		for n, c := range s.NeighborhoodVotes {
			if d.NeighborhoodVotes == nil {
				d.NeighborhoodVotes = make(map[string]int, len(s.NeighborhoodVotes))
			}
			d.NeighborhoodVotes[n] += c
		}
	}
}

// shard splits n items into at most w contiguous [lo,hi) ranges.
func shard(n, w int) [][2]int {
	if w < 1 {
		w = 1
	}
	size := (n + w - 1) / w
	var out [][2]int
	for lo := 0; lo < n; lo += size {
		out = append(out, [2]int{lo, min(lo+size, n)})
	}
	return out
}

// Build constructs the Index using `workers` goroutines per dataset.
// asOf is the reference time (Unix seconds) for "still open and overdue".
func Build(ds *model.Dataset, workers int, asOf int64) *Index {
	var (
		mu       sync.Mutex // guards partials only (append of a pointer, not the hot path)
		partials []*Index
		wg       sync.WaitGroup
	)
	spawn := func(fn func(*Index)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := newIndex(asOf)
			fn(p)
			mu.Lock()
			partials = append(partials, p)
			mu.Unlock()
		}()
	}
	for _, r := range shard(len(ds.Crimes), workers) {
		s := ds.Crimes[r[0]:r[1]]
		spawn(func(p *Index) { p.addCrimes(s) })
	}
	for _, r := range shard(len(ds.Requests), workers) {
		s := ds.Requests[r[0]:r[1]]
		spawn(func(p *Index) { p.addRequests(s) })
	}
	for _, r := range shard(len(ds.Vacants), workers) {
		s := ds.Vacants[r[0]:r[1]]
		spawn(func(p *Index) { p.addVacants(s) })
	}
	spawn(func(p *Index) { p.addLiquor(ds.Liquor) })
	wg.Wait()

	ix := newIndex(asOf)
	for _, p := range partials {
		ix.merge(p)
	}

	// Resolve each block's neighborhood by majority vote across datasets,
	// then roll liquor outlets (which carry no neighborhood) up to it.
	for _, b := range ix.Blocks {
		best, bestN := "", 0
		for n, c := range b.NeighborhoodVotes {
			if c > bestN || (c == bestN && n < best) {
				best, bestN = n, c
			}
		}
		b.Neighborhood = best
		b.NeighborhoodVotes = nil // no longer needed; release memory
		if b.Liquor > 0 && best != "" {
			ix.nb(best).Liquor += b.Liquor
		}
	}
	return ix
}
