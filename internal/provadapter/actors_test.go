package provadapter

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"
	_ "modernc.org/sqlite"
)

// TestOrdinalActorID_Goldens pins the fixed big-endian ordinal-UUID wire form for
// the reservation boundary ordinals 0, 1, and 1023, and rejects 1024.
func TestOrdinalActorID_Goldens(t *testing.T) {
	cases := []struct {
		ordinal  uint64
		wantUUID string
	}{
		{0, "00000000-0000-0000-0000-000000000000"},
		{1, "00000000-0000-0000-0000-000000000001"},
		{1023, "00000000-0000-0000-0000-0000000003ff"},
	}
	for _, tc := range cases {
		id, err := OrdinalActorID(tc.ordinal)
		if err != nil {
			t.Fatalf("OrdinalActorID(%d): %v", tc.ordinal, err)
		}
		if id.Namespace != PastureSystemNamespace {
			t.Fatalf("ordinal %d namespace = %q, want %q", tc.ordinal, id.Namespace, PastureSystemNamespace)
		}
		if id.UUID.String() != tc.wantUUID {
			t.Fatalf("ordinal %d uuid = %q, want %q", tc.ordinal, id.UUID.String(), tc.wantUUID)
		}
	}
	if _, err := OrdinalActorID(1024); err == nil {
		t.Fatalf("expected ordinal 1024 to be rejected (out of reserved range)")
	}
}

// TestPastureSystemDefault confirms ordinal zero is the all-zero-UUID default.
func TestPastureSystemDefault(t *testing.T) {
	def := PastureSystemDefaultActorID()
	if def.UUID != uuid.Nil {
		t.Fatalf("default UUID = %q, want the nil UUID", def.UUID.String())
	}
	zero, err := OrdinalActorID(0)
	if err != nil {
		t.Fatalf("OrdinalActorID(0): %v", err)
	}
	if zero != def {
		t.Fatalf("ordinal zero %+v != default %+v", zero, def)
	}
}

// TestValidateActorID proves the zero ActorID rejects while a namespaced all-zero
// UUID (the default identity) is valid.
func TestValidateActorID(t *testing.T) {
	if err := ValidateActorID(provenance.ActorID{}); err == nil {
		t.Fatalf("expected zero ActorID to be rejected")
	}
	if err := ValidateActorID(PastureSystemDefaultActorID()); err != nil {
		t.Fatalf("namespaced nil-UUID actor should be valid: %v", err)
	}
}

func openMemoryTracker(t *testing.T) provenance.Tracker {
	t.Helper()
	tr, err := provenance.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func assertDefaultSoftwareAgent(t *testing.T, tr provenance.Tracker) {
	t.Helper()
	agent, err := tr.SoftwareAgent(PastureSystemDefaultActorID())
	if err != nil {
		t.Fatalf("SoftwareAgent(default): %v", err)
	}
	if agent.ID != PastureSystemDefaultActorID() || agent.Name != PastureSystemDefaultName ||
		agent.Version != pastureSystemVersion || agent.Source != pastureSystemSource {
		t.Fatalf("default software agent = %+v, want id=%q name=%q version=%q source=%q",
			agent, PastureSystemDefaultActorID(), PastureSystemDefaultName, pastureSystemVersion, pastureSystemSource)
	}
}

// TestActivate_Fresh activates a real store from empty and atomically installs
// the exact claim, fixed software agent, and ordinal-zero manifest entry.
func TestActivate_Fresh(t *testing.T) {
	tr := openMemoryTracker(t)

	res, err := ActivatePastureSystem(tr)
	if err != nil {
		t.Fatalf("ActivatePastureSystem: %v", err)
	}
	if res.DefaultActorID != PastureSystemDefaultActorID() {
		t.Fatalf("default actor drift: %+v", res.DefaultActorID)
	}
	assertDefaultSoftwareAgent(t, tr)

	claims, err := tr.Journal().NamespaceClaims()
	if err != nil {
		t.Fatalf("NamespaceClaims: %v", err)
	}
	var found *provenance.ActorNamespaceClaim
	for i := range claims {
		if claims[i].Namespace == PastureSystemNamespace {
			found = &claims[i]
		}
	}
	if found == nil {
		t.Fatalf("pasture-system claim not registered")
	}
	if !found.Equal(PastureSystemClaim()) {
		t.Fatalf("registered claim %+v != manifest %+v", *found, PastureSystemClaim())
	}
}

// TestActivate_ExactRepeatInert proves a second exact activation is inert and
// returns the same actor without error.
func TestActivate_ExactRepeatInert(t *testing.T) {
	tr := openMemoryTracker(t)

	if _, err := ActivatePastureSystem(tr); err != nil {
		t.Fatalf("first activation: %v", err)
	}
	res, err := ActivatePastureSystem(tr)
	if err != nil {
		t.Fatalf("second activation should be inert: %v", err)
	}
	if res.DefaultActorID != PastureSystemDefaultActorID() {
		t.Fatalf("exact re-activation actor = %q", res.DefaultActorID)
	}
	assertDefaultSoftwareAgent(t, tr)
}

// TestActivate_DriftRejected proves a stored pasture-system claim that differs
// from the manifest aborts activation without creating the fixed actor.
func TestActivate_DriftRejected(t *testing.T) {
	tr := openMemoryTracker(t)

	drift := provenance.ActorNamespaceClaim{
		Namespace:  PastureSystemNamespace,
		ClaimantID: PastureSystemNamespace,
		Range:      provenance.UUIDRange{Min: [16]byte{}, Max: provenance.BigEndianUUID(10)},
		Codec:      provenance.OrdinalV1CodecName,
	}
	if err := tr.Journal().RegisterNamespaceClaim(drift); err != nil {
		t.Fatalf("seed drifted claim: %v", err)
	}
	if _, err := ActivatePastureSystem(tr); !errors.Is(err, provenance.ErrNamespaceClaim) {
		t.Fatalf("drift activation error = %v, want ErrNamespaceClaim", err)
	}
	if _, err := tr.SoftwareAgent(PastureSystemDefaultActorID()); !errors.Is(err, provenance.ErrNotFound) {
		t.Fatalf("default actor lookup after rejected drift = %v, want ErrNotFound", err)
	}
}

// TestActivate_RepairsClaimOnlyStore proves a persisted exact claim missing its
// assigned default converges through the same atomic production API.
func TestActivate_RepairsClaimOnlyStore(t *testing.T) {
	tr := openMemoryTracker(t)
	if err := tr.Journal().RegisterNamespaceClaim(PastureSystemClaim()); err != nil {
		t.Fatalf("seed exact claim: %v", err)
	}
	res, err := ActivatePastureSystem(tr)
	if err != nil {
		t.Fatalf("repair claim-only store: %v", err)
	}
	if res.DefaultActorID != PastureSystemDefaultActorID() {
		t.Fatalf("repair result = %+v, want default actor", res)
	}
	assertDefaultSoftwareAgent(t, tr)
}

// TestActivate_PersistedRetry proves file-backed activation survives close and
// reopen and an exact retry remains inert with the same deterministic actor.
func TestActivate_PersistedRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pasture.db")
	tr, err := provenance.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite(first): %v", err)
	}
	first, err := ActivatePastureSystem(tr)
	if err != nil {
		t.Fatalf("first activation: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("close first tracker: %v", err)
	}

	tr, err = provenance.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite(reopen): %v", err)
	}
	defer func() { _ = tr.Close() }()
	second, err := ActivatePastureSystem(tr)
	if err != nil {
		t.Fatalf("persisted retry: %v", err)
	}
	if first.DefaultActorID != second.DefaultActorID {
		t.Fatalf("activation results first=%+v second=%+v", first, second)
	}
	assertDefaultSoftwareAgent(t, tr)
}

// TestActivate_PreClaimActorRollsBack proves a conflicting fixed-ID actor that
// predates its namespace claim rejects activation without adding claim or
// manifest rows. The pre-existing actor itself remains untouched.
func TestActivate_PreClaimActorRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pasture.db")
	tr, err := provenance.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite(schema): %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("close schema tracker: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	id := PastureSystemDefaultActorID().String()
	if _, err := db.Exec(`INSERT INTO agents (id, kind_id) VALUES (?, ?)`, id, int(provenance.AgentKindSoftware)); err != nil {
		t.Fatalf("seed base actor: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO agents_software (agent_id, name, version, source) VALUES (?, ?, ?, ?)`,
		id, PastureSystemDefaultName, pastureSystemVersion, pastureSystemSource); err != nil {
		t.Fatalf("seed software actor: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	tr, err = provenance.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite(conflict): %v", err)
	}
	if _, err := ActivatePastureSystem(tr); !errors.Is(err, provenance.ErrAgentAlreadyExists) {
		t.Fatalf("pre-claim activation error = %v, want ErrAgentAlreadyExists", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("close conflict tracker: %v", err)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen fixture database: %v", err)
	}
	defer func() { _ = db.Close() }()
	counts := []struct {
		name  string
		query string
		want  int
	}{
		{"actor_namespace_claims", `SELECT COUNT(*) FROM actor_namespace_claims`, 0},
		{"agents", `SELECT COUNT(*) FROM agents`, 1},
		{"agents_software", `SELECT COUNT(*) FROM agents_software`, 1},
		{"fixed_actor_manifest_entries", `SELECT COUNT(*) FROM fixed_actor_manifest_entries`, 0},
	}
	for _, count := range counts {
		var got int
		if err := db.QueryRow(count.query).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", count.name, err)
		}
		if got != count.want {
			t.Errorf("%s rows = %d, want %d", count.name, got, count.want)
		}
	}
}

// TestActivate_ConcurrentStartup proves independent file-backed trackers racing
// activation all converge on one claim, one actor, and one manifest row.
func TestActivate_ConcurrentStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pasture.db")
	const writers = 8
	trackers := make([]provenance.Tracker, writers)
	for i := range trackers {
		var err error
		trackers[i], err = provenance.OpenSQLite(path)
		if err != nil {
			t.Fatalf("OpenSQLite(%d): %v", i, err)
		}
		t.Cleanup(func() { _ = trackers[i].Close() })
	}

	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for _, tr := range trackers {
		wg.Add(1)
		go func(tr provenance.Tracker) {
			defer wg.Done()
			<-start
			res, err := ActivatePastureSystem(tr)
			if err == nil && res.DefaultActorID != PastureSystemDefaultActorID() {
				err = errors.New("activation returned a non-default actor")
			}
			errs <- err
		}(tr)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent activation: %v", err)
		}
	}
	assertDefaultSoftwareAgent(t, trackers[0])
	claims, err := trackers[0].Journal().NamespaceClaims()
	if err != nil {
		t.Fatalf("NamespaceClaims: %v", err)
	}
	if len(claims) != 1 || !claims[0].Equal(PastureSystemClaim()) {
		t.Fatalf("claims after concurrent startup = %+v", claims)
	}
}

// TestActivate_NilTracker rejects a nil tracker.
func TestActivate_NilTracker(t *testing.T) {
	if _, err := ActivatePastureSystem(nil); err == nil {
		t.Fatal("expected nil-tracker rejection")
	}
}
