// Package timeouts owns the ordered timeout hierarchy shared by SQLite,
// lifecycle ingress, and slice workflows.
package timeouts

import (
	"fmt"
	"time"
)

type Kind uint8

const (
	Production Kind = iota + 1
	Test
)

// Profile is immutable and constructor-validated. SQLiteBusy is innermost and
// must complete before either caller deadline can expire.
type Profile struct {
	kind       Kind
	sqliteBusy time.Duration
	ingress    time.Duration
	startSlice time.Duration
}

func New(kind Kind, sqliteBusy, ingress, startSlice time.Duration) (Profile, error) {
	if kind != Production && kind != Test {
		return Profile{}, fmt.Errorf("timeout profile kind %d is unknown", kind)
	}
	if sqliteBusy <= 0 || ingress <= 0 || startSlice <= 0 {
		return Profile{}, fmt.Errorf("timeout profile durations must all be positive: sqlite=%s ingress=%s startSlice=%s", sqliteBusy, ingress, startSlice)
	}
	if sqliteBusy >= ingress || sqliteBusy >= startSlice {
		return Profile{}, fmt.Errorf("timeout profile is inverted: SQLite busy timeout %s must be strictly below ingress %s and start_slice %s", sqliteBusy, ingress, startSlice)
	}
	return Profile{kind: kind, sqliteBusy: sqliteBusy, ingress: ingress, startSlice: startSlice}, nil
}

// ProductionProfile allocates half of ingress to each SQLite lock wait. This
// preserves useful WAL contention absorption while leaving strict headroom
// below the one- and two-second caller windows.
func ProductionProfile() Profile {
	return must(New(Production, 500*time.Millisecond, time.Second, 2*time.Second))
}

// TestProfile scales the hierarchy around the required 250 ms ingress window.
// SQLite gets one tenth of ingress (25 ms), leaving room for the journal fold's
// multiple bounded lock acquisitions, and start_slice remains twice ingress.
func TestProfile() Profile {
	return must(New(Test, 25*time.Millisecond, 250*time.Millisecond, 500*time.Millisecond))
}

func KnownProfiles() []Profile              { return []Profile{ProductionProfile(), TestProfile()} }
func (p Profile) Kind() Kind                { return p.kind }
func (p Profile) SQLiteBusy() time.Duration { return p.sqliteBusy }
func (p Profile) Ingress() time.Duration    { return p.ingress }
func (p Profile) StartSlice() time.Duration { return p.startSlice }
func (p Profile) IsZero() bool              { return p.kind == 0 }
func (p Profile) Validate() error {
	_, err := New(p.kind, p.sqliteBusy, p.ingress, p.startSlice)
	return err
}

func must(profile Profile, err error) Profile {
	if err != nil {
		panic(err)
	}
	return profile
}
