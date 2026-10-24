package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

func seedProjectHandlerStore(t *testing.T) (dbPath, epochOne, epochTwo string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	epochOne = createHandlerProjectEpoch(t, tracker, "Epoch one")
	epochTwo = createHandlerProjectEpoch(t, tracker, "Epoch two")
	return dbPath, epochOne, epochTwo
}

func createHandlerProjectEpoch(t *testing.T, tracker protocol.TaskTracker, title string) string {
	t.Helper()
	epoch, err := tracker.Create("file://project-handler", title, "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	return epoch.ID.String()
}

func handlerProjectHash(seed string) string { return strings.Repeat(seed, 64/len(seed)) }

func decodeHandlerProjectPage(t *testing.T, raw string) sessionapi.ProjectPage {
	t.Helper()
	var page sessionapi.ProjectPage
	require.NoError(t, json.Unmarshal([]byte(raw), &page), "body: %s", raw)
	return page
}

func decodeHandlerProjectView(t *testing.T, raw string) sessionapi.ProjectView {
	t.Helper()
	var view sessionapi.ProjectView
	require.NoError(t, json.Unmarshal([]byte(raw), &view), "body: %s", raw)
	return view
}

func TestProjectHandlersJSONListShowBindUnbind(t *testing.T) {
	t.Parallel()
	dbPath, epochOne, epochTwo := seedProjectHandlerStore(t)
	hash := handlerProjectHash("a")
	ctx := t.Context()

	var bound bytes.Buffer
	code, err := handlers.ProjectBind(ctx, handlers.ProjectBindInput{
		DBPath:      dbPath,
		ProjectHash: hash,
		EpochID:     epochOne,
		Role:        "worker",
		Output:      &bound,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	view := decodeHandlerProjectView(t, bound.String())
	require.Equal(t, hash, view.ProjectHash)
	require.NotNil(t, view.Binding)
	require.Equal(t, sessionapi.ProjectBindingSourceProject, view.Binding.Source)
	require.Equal(t, sessionapi.ConfidenceVerified, view.Binding.Confidence)
	require.Equal(t, epochOne, view.Epoch.ID)
	require.Len(t, view.Intervals, 1)

	var list bytes.Buffer
	code, err = handlers.ProjectList(ctx, handlers.ProjectListInput{DBPath: dbPath, Output: &list}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	page := decodeHandlerProjectPage(t, list.String())
	require.Len(t, page.Items, 1)
	require.Equal(t, hash, page.Items[0].ProjectHash)

	var shown bytes.Buffer
	code, err = handlers.ProjectShow(ctx, handlers.ProjectShowInput{DBPath: dbPath, ProjectHash: hash, Output: &shown}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	require.Len(t, decodeHandlerProjectView(t, shown.String()).Intervals, 1)

	var rebound bytes.Buffer
	code, err = handlers.ProjectBind(ctx, handlers.ProjectBindInput{
		DBPath:      dbPath,
		ProjectHash: hash,
		EpochID:     epochTwo,
		Role:        "reviewer",
		Output:      &rebound,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	view = decodeHandlerProjectView(t, rebound.String())
	require.Equal(t, epochTwo, view.Epoch.ID)
	require.Len(t, view.Intervals, 2)
	require.Equal(t, "rebind", view.Intervals[0].EndReason)

	var text bytes.Buffer
	code, err = handlers.ProjectShow(ctx, handlers.ProjectShowInput{DBPath: dbPath, ProjectHash: hash, Output: &text}, types.OutputText)
	require.NoError(t, err)
	require.Zero(t, code)
	require.Contains(t, text.String(), hash)
	require.Contains(t, text.String(), "Epoch two")
	require.Contains(t, text.String(), "source=project")
	require.Contains(t, text.String(), "confidence=verified")
	require.Contains(t, text.String(), "Intervals:  2")

	var ended bytes.Buffer
	code, err = handlers.ProjectUnbind(ctx, handlers.ProjectUnbindInput{DBPath: dbPath, ProjectHash: hash, Output: &ended}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	view = decodeHandlerProjectView(t, ended.String())
	require.Nil(t, view.Binding)
	require.Len(t, view.Intervals, 2)
}

func TestProjectHandlersActionableErrors(t *testing.T) {
	t.Parallel()
	dbPath, epochOne, _ := seedProjectHandlerStore(t)
	hash := handlerProjectHash("b")
	ctx := t.Context()

	code, err := handlers.ProjectShow(ctx, handlers.ProjectShowInput{DBPath: dbPath, ProjectHash: "not-a-hash", Output: &bytes.Buffer{}}, types.OutputText)
	require.Error(t, err)
	require.Equal(t, 1, code, "a malformed hash is a validation error")
	var structured *pasterrors.StructuredError
	require.True(t, errors.As(err, &structured))
	require.Equal(t, pasterrors.CategoryValidation, structured.Category)

	code, err = handlers.ProjectBind(ctx, handlers.ProjectBindInput{DBPath: dbPath, ProjectHash: hash, EpochID: "file://missing", Output: &bytes.Buffer{}}, types.OutputText)
	require.Error(t, err)
	require.Equal(t, 3, code, "an unknown epoch is a workflow error")
	require.True(t, errors.As(err, &structured))
	require.Equal(t, pasterrors.CategoryWorkflow, structured.Category)

	code, err = handlers.ProjectUnbind(ctx, handlers.ProjectUnbindInput{DBPath: dbPath, ProjectHash: hash, Output: &bytes.Buffer{}}, types.OutputText)
	require.Error(t, err)
	require.Equal(t, 3, code, "unbinding without an active mapping is a workflow error")
	require.True(t, errors.As(err, &structured))
	require.Equal(t, pasterrors.CategoryWorkflow, structured.Category)

	_, err = handlers.ProjectBind(ctx, handlers.ProjectBindInput{DBPath: dbPath, ProjectHash: hash, EpochID: epochOne, ExpectedBinding: "stale", Output: &bytes.Buffer{}}, types.OutputJSON)
	require.Error(t, err)
	require.Equal(t, 3, pasterrors.ExitCode(err))
	require.True(t, errors.Is(err, tasks.ErrProjectBindingConflict))

	// A missing store is a connection error, and the command never creates one.
	missing := filepath.Join(t.TempDir(), "missing.db")
	code, err = handlers.ProjectList(ctx, handlers.ProjectListInput{DBPath: missing, Output: &bytes.Buffer{}}, types.OutputText)
	require.Error(t, err)
	require.Equal(t, 2, code)
	_, statErr := os.Stat(missing)
	require.True(t, os.IsNotExist(statErr), "project commands must not create a store")
}
