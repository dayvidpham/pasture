package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type modelFixtureTransport func(*http.Request) (*http.Response, error)

func (f modelFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestModelsRefreshRootHTTPFixtureAndJSONWithoutStore(t *testing.T) {
	// Serial shell test: route only this command's real Bestiary HTTP request to
	// a local fixture. No public endpoint override or live catalog is needed.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	bad := filepath.Join(home, "not-a-directory")
	require.NoError(t, os.WriteFile(bad, []byte("not a database"), 0o600))
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"fixture-provider":{"models":{"Exact/Alias":{"id":"Exact/Alias","name":"Fixture model"}}}}`))
	}))
	defer s.Close()
	localURL, err := url.Parse(s.URL)
	require.NoError(t, err)
	transport := http.DefaultTransport
	http.DefaultTransport = modelFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https://models.dev/api.json", r.URL.String())
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = localURL.Scheme, localURL.Host
		copy.URL = &u
		return transport.RoundTrip(copy)
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	oldDB, oldFormat, oldNS := flagDBPath, flagFormat, flagNamespace
	refresh, _, err := rootCmd.Find([]string{"models", "refresh"})
	require.NoError(t, err)
	refresh.SetContext(nil)
	t.Cleanup(func() {
		refresh.SetContext(nil)
		flagDBPath, flagFormat, flagNamespace = oldDB, oldFormat, oldNS
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetContext(context.Background())
	})
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--db", filepath.Join(bad, "never.db"), "models", "refresh", "--format", "json"})
	require.NoError(t, rootCmd.ExecuteContext(context.Background()))
	require.Equal(t, 1, calls)
	var result map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	require.Equal(t, "refresh", result["operation"])
	require.Equal(t, float64(1), result["model_count"])
	require.FileExists(t, result["path"].(string))
	require.NoFileExists(t, filepath.Join(bad, "never.db"))
}

func TestModelsRefreshRootUsesCommandContextWithoutOpeningStore(t *testing.T) {
	// The shared Cobra root and its bound flags are process-global; keep this serial.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	bad := filepath.Join(home, "not-a-directory")
	require.NoError(t, os.WriteFile(bad, []byte("not a database"), 0o600))
	oldDB, oldFormat, oldNS := flagDBPath, flagFormat, flagNamespace
	refresh, _, err := rootCmd.Find([]string{"models", "refresh"})
	require.NoError(t, err)
	refresh.SetContext(nil)
	transport := http.DefaultTransport
	http.DefaultTransport = modelFixtureTransport(func(*http.Request) (*http.Response, error) {
		t.Error("canceled command attempted HTTP")
		return nil, context.Canceled
	})
	t.Cleanup(func() {
		http.DefaultTransport = transport
		refresh.SetContext(nil)
		flagDBPath, flagFormat, flagNamespace = oldDB, oldFormat, oldNS
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetContext(context.Background())
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--db", filepath.Join(bad, "never.db"), "models", "refresh", "--format", "json"})
	err = rootCmd.ExecuteContext(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NoFileExists(t, filepath.Join(home, ".cache", "pasture", "models-v1.json"))
	require.NoFileExists(t, filepath.Join(bad, "never.db"))
}
