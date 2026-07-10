package bandwidthtop

import (
	"sync"

	"bandwidth-monitor/geoip"
)

type localDatabase interface {
	Available() bool
	Lookup(string) *geoip.Result
	Close()
}

type localDBOpener func(countryPath, asnPath string) (localDatabase, error)

type localOpenResult struct {
	db  localDatabase
	err error
}

// LocalDatabases owns independently opened geography and ASN readers. Opening
// them separately lets one optional database remain usable if the other fails.
type LocalDatabases struct {
	city      localDatabase
	asn       localDatabase
	cityState string
	asnState  string
	warnings  []string
	closeOnce sync.Once
}

func OpenLocalDatabases(cityPath, asnPath string) *LocalDatabases {
	return openLocalDatabases(cityPath, asnPath, func(countryPath, asnPath string) (localDatabase, error) {
		return geoip.Open(countryPath, asnPath)
	})
}

func openLocalDatabases(cityPath, asnPath string, opener localDBOpener) *LocalDatabases {
	dbs := &LocalDatabases{cityState: "not configured", asnState: "not configured"}
	var cityResult, asnResult localOpenResult
	var wait sync.WaitGroup
	if cityPath != "" {
		wait.Add(1)
		go func() {
			defer wait.Done()
			cityResult.db, cityResult.err = opener(cityPath, "")
		}()
	}
	if asnPath != "" {
		wait.Add(1)
		go func() {
			defer wait.Done()
			asnResult.db, asnResult.err = opener("", asnPath)
		}()
	}
	wait.Wait()
	dbs.city, dbs.cityState = checkedLocalDatabase(cityPath, cityResult, "city/country", &dbs.warnings)
	dbs.asn, dbs.asnState = checkedLocalDatabase(asnPath, asnResult, "ASN", &dbs.warnings)
	return dbs
}

func checkedLocalDatabase(path string, result localOpenResult, name string, warnings *[]string) (localDatabase, string) {
	if path == "" {
		return nil, "not configured"
	}
	if result.err != nil || result.db == nil || !result.db.Available() {
		if result.db != nil {
			result.db.Close()
		}
		*warnings = append(*warnings, name+" MMDB disabled: database could not be opened")
		return nil, "unavailable"
	}
	return result.db, "ready"
}

func (d *LocalDatabases) Close() {
	if d == nil {
		return
	}
	d.closeOnce.Do(func() {
		if d.city != nil {
			d.city.Close()
		}
		if d.asn != nil {
			d.asn.Close()
		}
	})
}

func (d *LocalDatabases) status() (string, bool) {
	if d == nil || d.cityState == "not configured" && d.asnState == "not configured" {
		return "not configured", false
	}
	if d.cityState == "ready" && d.asnState == "ready" {
		return "ready", true
	}
	if d.cityState == "ready" {
		return "partially ready (city ready, ASN " + d.asnState + ")", true
	}
	if d.asnState == "ready" {
		return "partially ready (city " + d.cityState + ", ASN ready)", true
	}
	return "unavailable", false
}

func newEnricherWithDatabases(cfg Config, cityPath, asnPath string, opener localDBOpener) (*Enricher, error) {
	var (
		enricher *Enricher
		err      error
		dbs      *LocalDatabases
		wait     sync.WaitGroup
	)
	wait.Add(2)
	go func() {
		defer wait.Done()
		enricher, err = NewEnricher(cfg)
	}()
	go func() {
		defer wait.Done()
		dbs = openLocalDatabases(cityPath, asnPath, opener)
	}()
	wait.Wait()
	if err != nil {
		dbs.Close()
		return nil, err
	}
	enricher.setLocalDatabases(dbs)
	return enricher, nil
}

// NewEnricherWithDatabases checks local databases and monitor readiness
// concurrently so startup is bounded by the slowest independent source.
func NewEnricherWithDatabases(cfg Config, cityPath, asnPath string) (*Enricher, error) {
	return newEnricherWithDatabases(cfg, cityPath, asnPath,
		func(countryPath, asnPath string) (localDatabase, error) {
			return geoip.Open(countryPath, asnPath)
		})
}
