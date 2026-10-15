// Package analysis contains the five stakeholder research questions. Each
// question is an independent Strategy implementing Analysis; the Runner
// executes all of them concurrently against the shared, read-only Dataset and
// Index.
package analysis

import (
	"sync"
	"time"

	"github.com/aidan/baltimore-engine/internal/index"
	"github.com/aidan/baltimore-engine/internal/model"
	"github.com/aidan/baltimore-engine/internal/report"
)

// Input bundles the immutable data every analysis reads. Nothing in Input is
// mutated after ingestion + indexing complete, so it is shared across
// goroutines without synchronisation (immutability as the thread-safety
// strategy).
type Input struct {
	DS *model.Dataset
	IX *index.Index
}

// Analysis is one research question.
type Analysis interface {
	Number() int
	Title() string
	Stakeholder() string
	Question() string
	Run(in *Input) []report.Element
}

// All returns the five analyses in report order.
func All() []Analysis {
	return []Analysis{
		VacancyViolence{},
		LiquorHotspots{},
		ServiceEquity{},
		BlightVacancy{},
		DistrictScorecard{},
	}
}

// RunAll executes analyses concurrently, one goroutine each, and returns the
// sections in their original order (each goroutine writes only its own slot
// of the pre-sized slice, so no mutex is required).
func RunAll(in *Input, as []Analysis) []report.Section {
	out := make([]report.Section, len(as))
	var wg sync.WaitGroup
	for i, a := range as {
		wg.Add(1)
		go func(i int, a Analysis) {
			defer wg.Done()
			start := time.Now()
			els := a.Run(in)
			out[i] = report.Section{
				Number:      a.Number(),
				Title:       a.Title(),
				Stakeholder: a.Stakeholder(),
				Question:    a.Question(),
				Elements:    els,
				Elapsed:     time.Since(start).Round(time.Microsecond),
			}
		}(i, a)
	}
	wg.Wait()
	return out
}
