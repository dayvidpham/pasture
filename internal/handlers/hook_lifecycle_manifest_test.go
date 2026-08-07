package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/tasks"
)

type metamodelReadView struct {
	ID                  string `json:"id"`
	Version             uint32 `json:"version"`
	Content             string `json:"content"`
	Journaled           bool   `json:"journaled"`
	DefinitionJournalID *int64 `json:"definitionJournalId"`
	Body                string `json:"body"`
}

// TestHookLifecycleMetamodelReadSurface exercises the production `hook lifecycle
// codebook` read surface end-to-end against a real store: before any delivery
// the active coordinate is reported unjournaled; after a valid delivery (which
// lazily activates the codebook) it is reported journaled with a definition
// journal id; and the emitted body is content-addressed (sha256(body) == the
// reported content digest == metamodel.Active().Content).
func TestHookLifecycleMetamodelReadSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	// Bootstrap the persisted system identity so deliveries can commit.
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = bootstrap.Create("file://codebook-read-test", "bootstrap", "initialize ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, bootstrap.Close())

	active := metamodel.Active()
	wantContent := hex.EncodeToString(active.Content[:])

	// Before any delivery: the active coordinate is reported, unjournaled.
	var before bytes.Buffer
	code, err := handlers.HookLifecycleMetamodel(ctx, &before, handlers.HookLifecycleMetamodelInput{DBPath: dbPath}, "json")
	require.NoError(t, err)
	require.Zero(t, code)
	var beforeView metamodelReadView
	require.NoError(t, json.Unmarshal(before.Bytes(), &beforeView))
	require.Equal(t, "pasture.lifecycle.metamodel", beforeView.ID)
	require.Equal(t, uint32(1), beforeView.Version)
	require.Equal(t, wantContent, beforeView.Content)
	require.False(t, beforeView.Journaled, "no delivery has activated the codebook yet")
	require.Nil(t, beforeView.DefinitionJournalID)

	// Deliver a valid Claude SessionStart, which lazily activates the metamodel.
	raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	require.NoError(t, err)
	_, err = handlers.HookLifecycleResponse(ctx, handlers.HookLifecycleInput{
		DBPath: dbPath, Harness: ir.HarnessClaudeCode, Event: "SessionStart", HostVersion: "2.1.210",
		Input: bytes.NewReader(raw), Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test.metamodel.delivery"},
	})
	require.NoError(t, err)

	// After delivery: the coordinate is journaled, and the body is content-addressed.
	var after bytes.Buffer
	code, err = handlers.HookLifecycleMetamodel(ctx, &after, handlers.HookLifecycleMetamodelInput{DBPath: dbPath, Body: true}, "json")
	require.NoError(t, err)
	require.Zero(t, code)
	var afterView metamodelReadView
	require.NoError(t, json.Unmarshal(after.Bytes(), &afterView))
	require.Equal(t, wantContent, afterView.Content)
	require.True(t, afterView.Journaled, "a valid delivery must have activated the codebook")
	require.NotNil(t, afterView.DefinitionJournalID)
	require.Greater(t, *afterView.DefinitionJournalID, int64(0))

	require.NotEmpty(t, afterView.Body)
	bodySum := sha256.Sum256([]byte(afterView.Body))
	require.Equal(t, wantContent, hex.EncodeToString(bodySum[:]), "codebook body must be content-addressed by the reported content digest")
}
