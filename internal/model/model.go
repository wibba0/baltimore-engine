// Package model holds the typed, normalised records produced by ingestion.
//
// Memory layout notes: records are stored by value in contiguous slices
// ([]Crime, []ServiceRequest, ...) rather than as []*Crime, so a million 311
// rows are one large allocation the GC scans linearly instead of a million
// small heap objects. Field types are chosen to be compact (float32 coords,
// uint8 enums, int64 Unix seconds instead of time.Time's 24 bytes), and all
// repeated strings are interned (see normalize.Interner).
package model

// CrimeCategory groups NIBRS offense codes.
type CrimeCategory uint8

const (
	CatOther CrimeCategory = iota
	CatViolent
	CatProperty
)

func (c CrimeCategory) String() string {
	switch c {
	case CatViolent:
		return "Violent"
	case CatProperty:
		return "Property"
	}
	return "Other"
}

// Crime is one NIBRS Group A incident.
type Crime struct {
	Time         int64 // Unix seconds, UTC
	Code         string
	Description  string
	Block        string // normalize.BlockKey
	Neighborhood string // normalize.NeighborhoodKey
	District     string
	Premise      string
	Lat, Lon     float32
	Hour         uint8 // local (America/New_York) hour of day
	Category     CrimeCategory
	Shooting     bool
}

// ServiceRequest is one 311 customer service request.
type ServiceRequest struct {
	Created, Closed, Due int64 // Unix seconds; 0 = null
	Type                 string
	Agency               string
	Method               string
	Status               string
	Block                string
	Neighborhood         string
	Lat, Lon             float32
	Council              uint8 // council district 1-14; 0 = unknown
}

// IsClosed reports whether the request has a usable close timestamp.
func (s *ServiceRequest) IsClosed() bool { return s.Closed > 0 && s.Closed >= s.Created }

// LiquorLicense is one (de-duplicated) liquor license for a license year.
type LiquorLicense struct {
	Key           string // LicenseClass + LicenseNumber
	TradeName     string
	Establishment string // Tavern, Package goods, Restaurant, ...
	Description   string // Beer, Wine & Liquor ...
	Address       string
	Block         string
	Zip           string
	Status        string
	Year          int16
}

// VacantNotice is one open Vacant Building Notice from DHCD.
type VacantNotice struct {
	Noticed      int64
	NoticeNum    string
	Address      string
	Block        string
	Neighborhood string
	Typology     string // 2023 Housing Market Typology
	Lat, Lon     float32
	Council      uint8
}

// IngestStats records per-dataset ingestion counters.
type IngestStats struct {
	Name     string
	File     string
	Format   string
	Rows     int64 // raw rows read
	Kept     int64 // rows that passed parsing and filtering
	Rejected int64
	Matched  int64 // rows with a usable block key
	Millis   int64
}

// Dataset is the fully ingested, immutable input to every analysis.
// After ingestion finishes it is only ever read, so analyses may share it
// across goroutines without locking.
type Dataset struct {
	Year     int
	Crimes   []Crime
	Requests []ServiceRequest
	Liquor   []LiquorLicense
	Vacants  []VacantNotice
	Stats    []IngestStats
}
