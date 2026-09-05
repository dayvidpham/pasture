package receipt_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// stampClock answers one fixed instant; it is the scripted clock a store is
// given so a test can choose the instant a Put stamps.
type stampClock struct{ now time.Time }

func (c stampClock) Now() time.Time { return c.now }

// openBlobStoreDB opens a fresh unified store, migrated to the current schema
// by the production opener, and returns the handle the production store
// writes through.
func openBlobStoreDB(t *testing.T) *sql.DB {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(filepath.Join(t.TempDir(), "pasture.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = tracker.Close() })
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	concrete, ok := reader.(projection.Reader)
	require.True(t, ok, "the production reader %T must expose the unified database handle", reader)
	require.NotNil(t, concrete.DB)
	return concrete.DB
}

func writtenAt(t *testing.T, db *sql.DB, ref digest.Digest) int64 {
	t.Helper()
	var stamp int64
	require.NoError(t, db.QueryRow(`SELECT written_at FROM lifecycle_payload_blobs WHERE digest = ?`, ref.String()).Scan(&stamp))
	return stamp
}

// TestPutStampsTheWriteInstantFromTheStoreClock: the stamp the orphan reclaim
// ages a blob against is the store clock's instant at the Put, in unix
// nanoseconds. A store that wrote no stamp would leave the column at its
// default of zero, which the reclaim reads as an UNKNOWN age and never
// reclaims, so every fresh orphan would then leak.
func TestPutStampsTheWriteInstantFromTheStoreClock(t *testing.T) {
	t.Parallel()
	db := openBlobStoreDB(t)
	instant := time.Unix(1_700_000_000, 123).UTC()
	store := receipt.NewSQLiteBlobStore(db, receipt.WithPayloadClock(stampClock{now: instant}))
	body := []byte(`{"hook_event_name":"Stop"}`)
	ref := digest.FromBytes(body)
	require.NoError(t, store.Put(context.Background(), ref, body))
	assert.Equal(t, instant.UnixNano(), writtenAt(t, db, ref), "written_at must be the store clock's instant at the Put, never the migration default")
}

// TestARepeatedPutRefreshesTheStampAndNeverMovesItBack: a writer that
// re-delivers a body whose digest is already stored is in flight between this
// Put and its journal append exactly like a first writer, so the stamp must
// follow the LATEST Put; and a clock that reads earlier than the stored stamp
// must not age the blob backwards.
func TestARepeatedPutRefreshesTheStampAndNeverMovesItBack(t *testing.T) {
	t.Parallel()
	db := openBlobStoreDB(t)
	body := []byte(`{"hook_event_name":"SessionStart"}`)
	ref := digest.FromBytes(body)
	first := time.Unix(1_700_000_000, 0).UTC()
	later := first.Add(45 * time.Second)
	earlier := first.Add(-45 * time.Second)

	require.NoError(t, receipt.NewSQLiteBlobStore(db, receipt.WithPayloadClock(stampClock{now: first})).Put(context.Background(), ref, body))
	require.Equal(t, first.UnixNano(), writtenAt(t, db, ref))

	require.NoError(t, receipt.NewSQLiteBlobStore(db, receipt.WithPayloadClock(stampClock{now: later})).Put(context.Background(), ref, body))
	assert.Equal(t, later.UnixNano(), writtenAt(t, db, ref), "a repeated Put of the same body must refresh written_at to the later instant; a stamp left at the first instant lets the reclaim delete the blob under the second writer's append")

	require.NoError(t, receipt.NewSQLiteBlobStore(db, receipt.WithPayloadClock(stampClock{now: earlier})).Put(context.Background(), ref, body))
	assert.Equal(t, later.UnixNano(), writtenAt(t, db, ref), "a Put whose clock reads earlier than the stored stamp must not move written_at back")

	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_payload_blobs WHERE digest = ?`, ref.String()).Scan(&count))
	assert.Equal(t, 1, count, "the blob stays content-addressed: one row per digest")
}

// TestAStoreBuiltAsALiteralStampsTheWallClock: production builds the store
// as SQLiteBlobStore{DB: db} with no clock, and that store must stamp real
// time. The stamp is bracketed by two wall-clock readings around the Put, a
// condition and not a wait.
func TestAStoreBuiltAsALiteralStampsTheWallClock(t *testing.T) {
	t.Parallel()
	db := openBlobStoreDB(t)
	body := []byte(`{"hook_event_name":"PreToolUse"}`)
	ref := digest.FromBytes(body)
	before := time.Now().UnixNano()
	require.NoError(t, (receipt.SQLiteBlobStore{DB: db}).Put(context.Background(), ref, body))
	after := time.Now().UnixNano()
	stamp := writtenAt(t, db, ref)
	assert.GreaterOrEqual(t, stamp, before, "a store with no scripted clock must stamp the wall clock, never zero")
	assert.LessOrEqual(t, stamp, after, "the stamp must be the instant of the Put, not a later reading")
}

// TestTheWriteDoorAndTheDeleteDoorStayNarrow pins the two interfaces the
// payload blob table is reached through. The write door, BlobStore, declares
// exactly Put, and the receipt service depends on nothing else of the store;
// the concrete store carries the write plus read-only inspection and NO
// delete; the delete door, PayloadReclaimer, declares exactly the bounded
// orphan delete, and only the projection rebuild's reclaim constructs it. A
// method added to either set is a new door and must be argued here.
func TestTheWriteDoorAndTheDeleteDoorStayNarrow(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"Put"}, methodNames(reflect.TypeOf((*receipt.BlobStore)(nil)).Elem()), "the write door declares exactly Put")
	assert.Equal(t, []string{"Exists", "Put", "Reclaimable", "ReclaimableCount"}, methodNames(reflect.TypeOf(receipt.SQLiteBlobStore{})), "the concrete store writes and inspects; a delete on it would make its doc false and the value unsafe to hand around")
	assert.Equal(t, []string{"ReclaimOrphansWrittenBefore"}, methodNames(reflect.TypeOf(receipt.PayloadReclaimer{})), "the delete door declares exactly the bounded orphan delete")
	assert.False(t, reflect.TypeOf(receipt.PayloadReclaimer{}).Implements(reflect.TypeOf((*receipt.BlobStore)(nil)).Elem()), "the delete door is not a write door")
}

func methodNames(typ reflect.Type) []string {
	names := make([]string, 0, typ.NumMethod())
	for index := 0; index < typ.NumMethod(); index++ {
		names = append(names, typ.Method(index).Name)
	}
	sort.Strings(names)
	return names
}
