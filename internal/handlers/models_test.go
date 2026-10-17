package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayvidpham/bestiary"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func testModelCatalog(t *testing.T) modelCatalog {
	t.Helper()
	dir := t.TempDir()
	return modelCatalog{
		allowDir: filepath.Join(dir, "allowed"), cachePath: filepath.Join(dir, "cache", "models-v1.json"),
		now: func() time.Time { return time.Date(2099, 1, 2, 3, 4, 5, 999, time.FixedZone("test", 3600)) },
		fetch: func(context.Context) ([]bestiary.ModelInfo, error) {
			t.Error("unexpected HTTP fetch for offline identity")
			return nil, errors.New("offline")
		},
		diagnostics: &bytes.Buffer{},
	}
}

func TestModelsExactPairBoundary(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{{"openai", "gpt-6.1-sol-ultrafast"}, {"openrouter", "openai/gpt-5"}, {"new-provider", "Exact-Alias"}} {
		require.NoError(t, validateModelPair(pair[0], pair[1]))
	}
	for _, pair := range [][2]string{{"", "x"}, {"a", ""}, {" a", "x"}, {"a", "x "}, {"a/b", "x"}, {"a#b", "x"}, {"a", "x\n"}, {"a", "x\x00"}, {"a", "x#high"}, {"a", "x#"}, {"a", "\xff"}} {
		require.Error(t, validateModelPair(pair[0], pair[1]), "%q", pair)
	}
	err := validateModelPair("openrouter", "openai/gpt-5#high")
	require.Contains(t, err.Error(), "cannot store execution variants")
	require.Contains(t, err.Error(), `--provider "openrouter" --model "openai/gpt-5"`)
}

func TestModelsAllowConcurrentAndSurvivesCacheEviction(t *testing.T) {
	t.Parallel()
	c := testModelCatalog(t)
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := range 24 {
		wg.Go(func() {
			model := "gpt-6.1-sol-ultrafast"
			if i%2 == 0 {
				model = "openai/gpt-5"
			}
			_, err := c.allow(context.Background(), "custom-provider", model)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	files, err := os.ReadDir(c.allowDir)
	require.NoError(t, err)
	require.Len(t, files, 2)
	info, err := os.Stat(c.allowDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	for _, f := range files {
		info, err := f.Info()
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	// A corrupt disposable cache must not revoke durable permission or static models.
	require.NoError(t, os.MkdirAll(filepath.Dir(c.cachePath), 0o700))
	require.NoError(t, os.WriteFile(c.cachePath, []byte("broken"), 0o600))
	for _, cached := range []bool{true, false} {
		if !cached {
			require.NoError(t, os.Remove(c.cachePath))
		}
		for _, model := range []string{"gpt-6.1-sol-ultrafast", "openai/gpt-5"} {
			r, err := c.resolve(context.Background(), "custom-provider", model)
			require.NoError(t, err)
			_, ok := r.Lookup("custom-provider", model)
			require.True(t, ok)
		}
		m := bestiary.Models()[0]
		_, err := c.resolve(context.Background(), string(m.Provider), string(m.ID))
		require.NoError(t, err)
	}
	require.Contains(t, c.diagnostics.(*bytes.Buffer).String(), c.cachePath)
}

func TestModelsAllowanceCorruptionAndUnsafeDestination(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"json", "version", "filename", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			c := testModelCatalog(t)
			path, err := c.allow(context.Background(), "test", "exact")
			require.NoError(t, err)
			switch kind {
			case "json":
				require.NoError(t, os.WriteFile(path, []byte(`{"version":1}`), 0o600))
			case "version":
				require.NoError(t, os.WriteFile(path, []byte(`{"version":2,"provider":"test","model":"exact"}`), 0o600))
			case "filename":
				require.NoError(t, os.Rename(path, filepath.Join(c.allowDir, "wrong.json")))
			case "symlink":
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Symlink(c.cachePath, path))
			case "directory":
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Mkdir(path, 0o700))
			}
			_, err = c.registry()
			require.Error(t, err)
			require.Contains(t, err.Error(), c.allowDir)
			require.Contains(t, err.Error(), "move malformed data aside")
			if kind == "symlink" || kind == "directory" {
				_, err = c.allow(context.Background(), "test", "exact")
				require.Error(t, err)
			}
		})
	}
}

func fixtureModelFetcher(t *testing.T, body string, status int) (func(context.Context) ([]bestiary.ModelInfo, error), *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api.json" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return newModelFetcher(bestiary.WithBaseURL(s.URL + "/api.json")), calls
}

func TestModelsHTTPRefreshAndMissRegistration(t *testing.T) {
	t.Parallel()
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "miss", true: "explicit"}[explicit], func(t *testing.T) {
			t.Parallel()
			c := testModelCatalog(t)
			fetch, calls := fixtureModelFetcher(t, `{"fixture-provider":{"models":{"Exact/Alias":{"id":"Exact/Alias","name":"Fixture model","limit":{"context":123456,"output":4096}}}}}`, http.StatusOK)
			c.fetch = fetch
			if explicit {
				var out bytes.Buffer
				require.NoError(t, modelsRefresh(context.Background(), &out, types.OutputJSON, c))
				var result map[string]any
				require.NoError(t, json.Unmarshal(out.Bytes(), &result))
				require.Equal(t, "refresh", result["operation"])
				require.Equal(t, float64(1), result["model_count"])
			}
			in := TaskAgentRegisterInput{Kind: AgentRegisterML, DBPath: filepath.Join(t.TempDir(), "tasks.db"), Namespace: "fixture", Role: "worker", Provider: "fixture-provider", Model: "Exact/Alias"}
			var out bytes.Buffer
			code, err := registerMLAgent(context.Background(), &out, in, types.OutputJSON, c)
			require.NoError(t, err)
			require.Zero(t, code)
			require.Contains(t, out.String(), "fixture/")
			require.Equal(t, int32(1), calls.Load())
			// Reopening reads the same durable snapshot offline, and duplicate identity semantics stay unchanged.
			c.fetch = testModelCatalog(t).fetch
			out.Reset()
			code, err = registerMLAgent(context.Background(), &out, in, types.OutputJSON, c)
			require.Error(t, err)
			require.Equal(t, 1, code)
			require.Contains(t, err.Error(), "already")
			data, err := os.ReadFile(c.cachePath)
			require.NoError(t, err)
			var snap modelSnapshot
			require.NoError(t, json.Unmarshal(data, &snap))
			require.Equal(t, "2099-01-02T02:04:05Z", snap.Timestamp)
			require.Equal(t, snap.Timestamp, snap.Models[0].LastSynced)
		})
	}
}

func TestModelsRefreshFailurePreservesSnapshotAndNoAuthor(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"http", "parse", "omitted", "cancel", "timeout", "publish"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			c := testModelCatalog(t)
			c.fetch, _ = fixtureModelFetcher(t, `{}`, http.StatusOK)
			_, err := c.refresh(context.Background())
			require.NoError(t, err)
			before, err := os.ReadFile(c.cachePath)
			require.NoError(t, err)
			ctx := context.Background()
			switch kind {
			case "http", "parse":
				status := http.StatusOK
				if kind == "http" {
					status = http.StatusServiceUnavailable
				}
				var calls *atomic.Int32
				c.fetch, calls = fixtureModelFetcher(t, `broken`, status)
				t.Cleanup(func() { require.Equal(t, int32(1), calls.Load(), "no retries") })
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "timeout":
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
				defer s.Close()
				c.fetch = newModelFetcher(bestiary.WithBaseURL(s.URL))
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			case "publish":
				// Refuse a nonregular destination before replacement; target bytes survive.
				target := c.cachePath
				c.cachePath += ".link"
				require.NoError(t, os.Symlink(target, c.cachePath))
			}
			in := TaskAgentRegisterInput{Kind: AgentRegisterML, DBPath: filepath.Join(t.TempDir(), "never.db"), Namespace: "fixture", Role: "worker", Provider: "missing", Model: "missing"}
			var out bytes.Buffer
			_, err = registerMLAgent(ctx, &out, in, types.OutputJSON, c)
			require.Error(t, err)
			require.NoFileExists(t, in.DBPath)
			require.Empty(t, out.String())
			after, err := os.ReadFile(c.cachePath)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestModelsMetadataPrecedenceAndTimestampValidation(t *testing.T) {
	t.Parallel()
	c := testModelCatalog(t)
	m := bestiary.Models()[0]
	m.DisplayName = "Fresh rich metadata"
	c.fetch = func(context.Context) ([]bestiary.ModelInfo, error) { return []bestiary.ModelInfo{m}, nil }
	_, err := c.allow(context.Background(), string(m.Provider), string(m.ID))
	require.NoError(t, err)
	_, err = c.refresh(context.Background())
	require.NoError(t, err)
	r, err := c.registry()
	require.NoError(t, err)
	got, ok := r.Lookup(provenance.Provider(m.Provider), string(m.ID))
	require.True(t, ok)
	require.Equal(t, "Fresh rich metadata", got.DisplayName)
	// Equal timestamps keep the static record; permission never erases metadata.
	static := bestiary.Models()[0]
	stamp, err := time.Parse(time.RFC3339, static.LastSynced)
	require.NoError(t, err)
	c.now = func() time.Time { return stamp }
	_, err = c.refresh(context.Background())
	require.NoError(t, err)
	r, err = c.registry()
	require.NoError(t, err)
	got, ok = r.Lookup(provenance.Provider(m.Provider), string(m.ID))
	require.True(t, ok)
	require.Equal(t, static.DisplayName, got.DisplayName)
	for _, stamp := range []string{"bad", "2099-01-02T02:04:05.1Z", "2099-01-02T03:04:05+01:00"} {
		require.Error(t, validSnapshot(modelSnapshot{Version: 1, Timestamp: stamp}))
	}
	require.Error(t, validSnapshot(modelSnapshot{Version: 1, Timestamp: "2099-01-02T02:04:05Z", Models: []bestiary.ModelInfo{m}}))
}

func TestModelsRefreshBudgetAndCancellationBeforePublication(t *testing.T) {
	t.Parallel()
	c := testModelCatalog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.fetch = func(fetchCtx context.Context) ([]bestiary.ModelInfo, error) {
		deadline, ok := fetchCtx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
		require.Greater(t, time.Until(deadline), 4*time.Second)
		cancel()
		return []bestiary.ModelInfo{{Provider: "fixture", ID: "exact"}}, nil
	}
	_, err := c.refresh(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NoFileExists(t, c.cachePath)
	_, err = findDuplicateAgentIDContext(ctx, filepath.Join(t.TempDir(), "never.db"), "fixture", TaskAgentRegisterInput{Kind: AgentRegisterML})
	require.ErrorIs(t, err, context.Canceled)
}

func TestModelsAllowedTargetRegistrationOnExistingStore(t *testing.T) {
	t.Parallel()
	c := testModelCatalog(t)
	for _, pair := range [][2]string{{"openai", "gpt-6.1-sol-ultrafast"}, {"new-provider", "openai/gpt-5"}} {
		_, err := c.allow(context.Background(), pair[0], pair[1])
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "old.db")
		tr, err := tasks.OpenTaskTracker(path)
		require.NoError(t, err)
		human, err := tr.RegisterHumanAgent("fixture", "Original", "original@example.test")
		require.NoError(t, err)
		require.NoError(t, tr.Close())
		in := TaskAgentRegisterInput{Kind: AgentRegisterML, DBPath: path, Namespace: "fixture", Role: "architect", Provider: pair[0], Model: pair[1]}
		var out bytes.Buffer
		code, err := registerMLAgent(context.Background(), &out, in, types.OutputJSON, c)
		require.NoError(t, err)
		require.Zero(t, code)
		tr, err = tasks.OpenTaskTracker(path)
		require.NoError(t, err)
		preserved, err := tr.HumanAgent(human.ID)
		require.NoError(t, err)
		require.Equal(t, human, preserved)
		require.NoError(t, tr.Close())
	}
}
