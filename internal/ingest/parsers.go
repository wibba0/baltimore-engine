package ingest

import (
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embed tz database so America/New_York works on any machine

	"github.com/aidan/baltimore-engine/internal/model"
	"github.com/aidan/baltimore-engine/internal/normalize"
)

// Baltimore is the local zone used for hour-of-day and calendar-year logic.
var Baltimore = mustLoc("America/New_York")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return l
}

// Parsers holds per-dataset parse strategies plus the shared interner.
type Parsers struct {
	Year int
	In   *normalize.Interner
}

func get(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

func f32(s string) float32 {
	v, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0
	}
	return float32(v)
}

func council(s string) uint8 {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 14 {
		return 0
	}
	return uint8(n)
}

func yearOf(unix int64) int { return time.Unix(unix, 0).In(Baltimore).Year() }

// CrimeCategoryOf maps NIBRS Group A offense codes to analysis categories.
// Violent = homicide, sex offenses, robbery, aggravated assault, kidnapping,
// human trafficking (the FBI "crimes against persons" serious subset).
func CrimeCategoryOf(code string) model.CrimeCategory {
	switch {
	case code == "", code == "13B", code == "13C":
		return model.CatOther // common assault / intimidation tracked separately
	case strings.HasPrefix(code, "09"), strings.HasPrefix(code, "11"),
		code == "120", code == "13A", code == "100", strings.HasPrefix(code, "64"):
		return model.CatViolent
	case code == "200", code == "210", code == "220", strings.HasPrefix(code, "23"),
		code == "240", code == "250", strings.HasPrefix(code, "26"),
		code == "270", code == "280", code == "290":
		return model.CatProperty
	}
	return model.CatOther
}

// Crime parses NIBRS rows, keeping incidents in the analysis year.
// CrimeDateTime is published in UTC; hour-of-day and year are computed in
// Baltimore local time (the default-midnight spike lands at 04/05 UTC, which
// confirms the source is true UTC).
func (p *Parsers) Crime(h *Header) ParseFunc[model.Crime] {
	cDT, cCode, cDesc := h.Col("CrimeDateTime"), h.Col("CrimeCode"), h.Col("Description")
	cLoc, cNbhd, cDist := h.Col("Location"), h.Col("Neighborhood"), h.Col("New_District")
	cLat, cLon, cShoot, cPrem := h.Col("Latitude"), h.Col("Longitude"), h.Col("Shooting"), h.Col("PremiseType")
	return func(row []string) (model.Crime, bool) {
		ts := normalize.ParseTime(get(row, cDT))
		if ts == 0 {
			return model.Crime{}, false
		}
		local := time.Unix(ts, 0).In(Baltimore)
		if local.Year() != p.Year {
			return model.Crime{}, false
		}
		code := get(row, cCode)
		return model.Crime{
			Time:         ts,
			Hour:         uint8(local.Hour()),
			Code:         p.In.Intern(code),
			Description:  p.In.Intern(get(row, cDesc)),
			Block:        p.In.Intern(normalize.BlockKey(get(row, cLoc))),
			Neighborhood: p.In.Intern(normalize.NeighborhoodKey(get(row, cNbhd))),
			District:     p.In.Intern(get(row, cDist)),
			Premise:      p.In.Intern(get(row, cPrem)),
			Lat:          f32(get(row, cLat)),
			Lon:          f32(get(row, cLon)),
			Category:     CrimeCategoryOf(code),
			Shooting:     strings.EqualFold(get(row, cShoot), "Y"),
		}, true
	}
}

// ServiceRequest parses 311 rows created in the analysis year, skipping
// soft-deleted records.
func (p *Parsers) ServiceRequest(h *Header) ParseFunc[model.ServiceRequest] {
	cCreated, cClosed, cDue := h.Col("CreatedDate"), h.Col("CloseDate"), h.Col("DueDate")
	cType, cAgency, cMethod, cStatus := h.Col("SRType"), h.Col("Agency"), h.Col("MethodReceived"), h.Col("SRStatus")
	cAddr, cNbhd, cCouncil := h.Col("Address"), h.Col("Neighborhood"), h.Col("CouncilDistrict")
	cLat, cLon, cDel := h.Col("Latitude"), h.Col("Longitude"), h.Col("IsDeleted")
	return func(row []string) (model.ServiceRequest, bool) {
		if get(row, cDel) == "1" {
			return model.ServiceRequest{}, false
		}
		created := normalize.ParseTime(get(row, cCreated))
		if created == 0 || yearOf(created) != p.Year {
			return model.ServiceRequest{}, false
		}
		return model.ServiceRequest{
			Created:      created,
			Closed:       normalize.ParseTime(get(row, cClosed)),
			Due:          normalize.ParseTime(get(row, cDue)),
			Type:         p.In.Intern(get(row, cType)),
			Agency:       p.In.Intern(get(row, cAgency)),
			Method:       p.In.Intern(get(row, cMethod)),
			Status:       p.In.Intern(get(row, cStatus)),
			Block:        p.In.Intern(normalize.BlockKey(get(row, cAddr))),
			Neighborhood: p.In.Intern(normalize.NeighborhoodKey(get(row, cNbhd))),
			Council:      council(get(row, cCouncil)),
			Lat:          f32(get(row, cLat)),
			Lon:          f32(get(row, cLon)),
		}, true
	}
}

// Liquor parses Liquor Board license rows for the analysis license year.
// The raw table has one row per licensee (person) — de-duplication to one
// row per license happens after the parallel parse (see DedupeLiquor).
func (p *Parsers) Liquor(h *Header) ParseFunc[model.LiquorLicense] {
	cClass, cNum, cYear := h.Col("LicenseClass"), h.Col("LicenseNumber"), h.Col("LicenseYear")
	cTrade, cEst, cDesc := h.Col("TradeName"), h.Col("EstablishmentDesc"), h.Col("Description")
	cAddr, cZip, cStatus := h.Col("AddrStreet"), h.Col("AddrZip"), h.Col("LicenseStatus")
	return func(row []string) (model.LiquorLicense, bool) {
		y, err := strconv.Atoi(get(row, cYear))
		if err != nil || y != p.Year {
			return model.LiquorLicense{}, false
		}
		addr := get(row, cAddr)
		return model.LiquorLicense{
			Key:           get(row, cClass) + " " + get(row, cNum),
			TradeName:     strings.Clone(get(row, cTrade)),
			Establishment: p.In.Intern(get(row, cEst)),
			Description:   p.In.Intern(get(row, cDesc)),
			Address:       strings.Clone(addr),
			Block:         p.In.Intern(normalize.BlockKey(addr)),
			Zip:           p.In.Intern(get(row, cZip)),
			Status:        p.In.Intern(get(row, cStatus)),
			Year:          int16(y),
		}, true
	}
}

// DedupeLiquor keeps the first row per license key (order-preserving).
func DedupeLiquor(in []model.LiquorLicense) []model.LiquorLicense {
	seen := make(map[string]struct{}, len(in))
	out := in[:0] // filter in place: no second allocation
	for _, l := range in {
		if _, ok := seen[l.Key]; ok {
			continue
		}
		seen[l.Key] = struct{}{}
		out = append(out, l)
	}
	return out
}

// Vacant parses open Vacant Building Notices (GeoJSON).
func (p *Parsers) Vacant(h *Header) ParseFunc[model.VacantNotice] {
	cNum, cDate, cAddr := h.Col("NoticeNum"), h.Col("DateNotice"), h.Col("Address")
	cNbhd, cCouncil, cTyp := h.Col("Neighborhood"), h.Col("Council_District"), h.Col("HousingMarketTypology2023")
	cLon, cLat := h.Col("_lon"), h.Col("_lat")
	return func(row []string) (model.VacantNotice, bool) {
		addr := get(row, cAddr)
		if addr == "" {
			return model.VacantNotice{}, false
		}
		return model.VacantNotice{
			Noticed:      normalize.ParseTime(get(row, cDate)),
			NoticeNum:    strings.Clone(get(row, cNum)),
			Address:      strings.Clone(addr),
			Block:        p.In.Intern(normalize.BlockKey(addr)),
			Neighborhood: p.In.Intern(normalize.NeighborhoodKey(get(row, cNbhd))),
			Typology:     p.In.Intern(get(row, cTyp)),
			Council:      council(get(row, cCouncil)),
			Lon:          f32(get(row, cLon)),
			Lat:          f32(get(row, cLat)),
		}, true
	}
}
