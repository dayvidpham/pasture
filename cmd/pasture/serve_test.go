package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/peasantproject"
)

// The main-only race job runs this package under heavy instrumentation and CI
// CPU contention. These waits bound a real startup or shutdown, not a latency
// requirement, so keep them generous enough that a slow runner cannot turn a
// healthy serve process into a false failure.
const (
	serveStartupBudget  = 60 * time.Second
	serveShutdownBudget = 30 * time.Second
)

// The serve help is the operator's route map. It must name the transcript
// proxy, the peasant dependency that rail has, and the ordinary auto-on-open
// migration path; the old text claimed serve never migrated the schema, which
// the tracker open contradicts.
func TestServeHelpDescribesEverySurface(t *testing.T) {
	var out bytes.Buffer
	previousOut := serveCmd.OutOrStdout()
	previousErr := serveCmd.ErrOrStderr()
	serveCmd.SetOut(&out)
	serveCmd.SetErr(&out)
	t.Cleanup(func() {
		serveCmd.SetOut(previousOut)
		serveCmd.SetErr(previousErr)
	})

	require.NoError(t, serveCmd.Help())
	help := out.String()
	for _, want := range []string{
		"GET  /                      the compiled web console",
		"web console is embedded",
		"no Node process",
		"--web-dir",
		"API remains at",
		"/api/v1/sessions",
		"/transcript",
		"peasant",
		"PATH",
		"503",
		"bind",
		"unbind",
		"/api/v1/epochs",
		"/api/v1/epochs/{id}/sessions",
		"/api/v1/projects",
		"/api/v1/projects/{projectHash}",
		"/api/v1/projects/{projectHash}/bind",
		"/api/v1/projects/{projectHash}/unbind",
		"/api/v1/inspect",
		"POST /api/v1/settings/peasant",
		"/api/v1/ws",
		"/openapi.json",
		"auto-on-open",
		"never runs a",
		"pasture migrate",
		"non-loopback",
		"--backfill",
		"--peasant-db",
		"--peasant-url",
		"Peasant project enrichment is opt-in",
		"can turn it on and off at runtime",
		"switch lasts for",
		"restarting returns to the startup default",
		"Nothing is opened while the toggle is off",
		"setting both is an",
	} {
		require.Contains(t, help, want)
	}
	require.Equal(t, "Serve the local session and epoch API and web console", serveCmd.Short,
		"the short line must name both surfaces this command serves")
	require.Contains(t, help, "session and epoch API")
	require.NotContains(t, help, "read API")
	require.NotContains(t, help, "never migrates the schema")
	flag := serveCmd.Flags().Lookup("web-dir")
	require.NotNil(t, flag)
	require.Empty(t, flag.DefValue, "the embedded bundle is the production default")
}

func TestServeBindsEphemeralLoopbackAndShutsDown(t *testing.T) {
	previous := flagDBPath
	flagDBPath = filepath.Join(t.TempDir(), "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	cmd := &cobra.Command{}
	cmd.Flags().String("listen", "127.0.0.1:0", "")
	cmd.Flags().Duration("active-window", 15*time.Minute, "")
	cmd.Flags().Bool("backfill", false, "")
	cmd.Flags().String("peasant-db", "", "")
	cmd.Flags().String("peasant-url", "", "")
	cmd.Flags().String("web-dir", "", "")
	cmd.SetArgs(nil)
	reader, writer := io.Pipe()
	cmd.SetOut(writer)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- runServe(cmd) }()
	lineCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reader).ReadString('\n')
		lineCh <- line
	}()

	var line string
	select {
	case line = <-lineCh:
	case <-time.After(serveStartupBudget):
		t.Fatal("serve did not print its bound address")
	}
	require.True(t, strings.HasPrefix(line, "pasture serve listening on http://127.0.0.1:"), line)
	base := strings.TrimSpace(strings.TrimPrefix(line, "pasture serve listening on "))

	response, err := http.Get(base + "/api/v1/sessions")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	// The compiled console is embedded in the test binary through the same
	// production package, so this proves the default serve path is UI+API.
	ui, err := http.Get(base + "/")
	require.NoError(t, err)
	defer ui.Body.Close()
	require.Equal(t, http.StatusOK, ui.StatusCode)
	uiBody, err := io.ReadAll(ui.Body)
	require.NoError(t, err)
	require.Contains(t, string(uiBody), `id="root"`, "the embedded app shell must be served at /")
	require.Contains(t, ui.Header.Get("Content-Type"), "text/html")
	require.Equal(t, "no-store", ui.Header.Get("Cache-Control"))

	clientRoute, err := http.Get(base + "/sessions/example")
	require.NoError(t, err)
	defer clientRoute.Body.Close()
	require.Equal(t, http.StatusOK, clientRoute.StatusCode)
	clientBody, err := io.ReadAll(clientRoute.Body)
	require.NoError(t, err)
	require.Contains(t, string(clientBody), `id="root"`, "client routes must fall back to the app shell")

	// The first two requests prove the outer wrapper covers the console, which
	// has no other guard; the third pins the API contract rather than
	// distinguishing the wrapper.
	status := func(request *http.Request) int {
		t.Helper()
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		return response.StatusCode
	}
	hostEvil, err := http.NewRequest(http.MethodGet, base+"/", nil)
	require.NoError(t, err)
	hostEvil.Host = "evil.example"
	require.Equal(t, http.StatusForbidden, status(hostEvil), "non-loopback Host must be refused for the console")

	originEvil, err := http.NewRequest(http.MethodGet, base+"/", nil)
	require.NoError(t, err)
	originEvil.Header.Set("Origin", "https://evil.example")
	require.Equal(t, http.StatusForbidden, status(originEvil), "non-loopback Origin must be refused for the console")

	apiHostEvil, err := http.NewRequest(http.MethodGet, base+"/api/v1/sessions", nil)
	require.NoError(t, err)
	apiHostEvil.Host = "evil.example"
	require.Equal(t, http.StatusForbidden, status(apiHostEvil), "the deliberate outer guard must cover the API too")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(serveShutdownBudget):
		t.Fatal("serve did not shut down after context cancellation")
	}
}

// TestServeWebDirOverride proves --web-dir replaces the embedded bundle with an
// external build on disk while the API stays mounted on the same origin.
func TestServeWebDirOverride(t *testing.T) {
	previous := flagDBPath
	flagDBPath = filepath.Join(t.TempDir(), "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	webDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(webDir, "index.html"),
		[]byte(`<!doctype html><html><body><div id="root">external build</div></body></html>`), 0o600))

	cmd := &cobra.Command{}
	cmd.Flags().String("listen", "127.0.0.1:0", "")
	cmd.Flags().Duration("active-window", 15*time.Minute, "")
	cmd.Flags().Bool("backfill", false, "")
	cmd.Flags().String("peasant-db", "", "")
	cmd.Flags().String("peasant-url", "", "")
	cmd.Flags().String("web-dir", webDir, "")
	require.NoError(t, cmd.Flags().Set("web-dir", webDir))
	cmd.SetArgs(nil)
	reader, writer := io.Pipe()
	cmd.SetOut(writer)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- runServe(cmd) }()
	type readResult struct {
		line string
		err  error
	}
	lineCh := make(chan readResult, 1)
	go func() {
		line, err := bufio.NewReader(reader).ReadString('\n')
		lineCh <- readResult{line: line, err: err}
	}()

	var line string
	select {
	case result := <-lineCh:
		require.NoError(t, result.err)
		line = result.line
	case <-time.After(serveStartupBudget):
		t.Fatal("serve did not print its bound address")
	}
	require.True(t, strings.HasPrefix(line, "pasture serve listening on http://127.0.0.1:"), line)
	base := strings.TrimSpace(strings.TrimPrefix(line, "pasture serve listening on "))

	ui, err := http.Get(base + "/")
	require.NoError(t, err)
	defer ui.Body.Close()
	require.Equal(t, http.StatusOK, ui.StatusCode)
	body, err := io.ReadAll(ui.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "external build")

	api, err := http.Get(base + "/api/v1/sessions")
	require.NoError(t, err)
	defer api.Body.Close()
	require.Equal(t, http.StatusOK, api.StatusCode)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(serveShutdownBudget):
		t.Fatal("serve did not shut down after context cancellation")
	}
}

// TestServeRejectsBadWebDirBeforeOpeningStore pins the early console check: a
// --web-dir with no index.html must fail before the store is created/migrated.
func TestServeRejectsBadWebDirBeforeOpeningStore(t *testing.T) {
	previous := flagDBPath
	flagDBPath = filepath.Join(t.TempDir(), "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	webDir := t.TempDir()
	cmd := &cobra.Command{}
	cmd.Flags().String("listen", "127.0.0.1:0", "")
	cmd.Flags().Duration("active-window", 15*time.Minute, "")
	cmd.Flags().Bool("backfill", false, "")
	cmd.Flags().String("peasant-db", "", "")
	cmd.Flags().String("peasant-url", "", "")
	cmd.Flags().String("web-dir", webDir, "")
	require.NoError(t, cmd.Flags().Set("web-dir", webDir))

	err := runServe(cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no index.html")
	require.Contains(t, err.Error(), "at its root")
	require.Contains(t, err.Error(), "omit --web-dir")
	require.NotContains(t, err.Error(), "rebuild pasture")
	_, statErr := os.Stat(flagDBPath)
	require.True(t, os.IsNotExist(statErr), "console validation must run before the store is opened")
}

// TestServeDefaultPeasantSourceEnablesTheConfiguredDatabase is the success path
// for the no-flag mode: the toggle lazily opens the conventional database under
// XDG_DATA_HOME, resolves a seeded session, and disabling clears lookups.
func TestServeDefaultPeasantSourceEnablesTheConfiguredDatabase(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	dir := filepath.Join(xdg, "peasant")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "peasant.db")
	db, err := sql.Open("sqlite", "file:"+path)
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TABLE projects (project_hash TEXT PRIMARY KEY, canonical_cwd TEXT, canonical_remote TEXT);
		CREATE TABLE sessions (session_id TEXT PRIMARY KEY, model_harness TEXT NOT NULL, project_hash TEXT NOT NULL, started_at INTEGER)`)
	require.NoError(t, err)
	hash := strings.Repeat("a", 64)
	_, err = db.Exec(`INSERT INTO projects VALUES (?, '/work/repo', 'github.com/org/repo')`, hash)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO sessions VALUES ('default-session', 'claude-code', ?, 1)`, hash)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	projectSwitch, err := newPeasantProjectSource("", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = projectSwitch.Close() })
	require.False(t, projectSwitch.Status().Enabled, "no flags starts off")

	key := peasantproject.Key{Harness: "claude-code", Session: "default-session"}
	projects, err := projectSwitch.Lookup(context.Background(), []peasantproject.Key{key})
	require.NoError(t, err)
	require.Empty(t, projects, "off performs no Peasant access")

	require.NoError(t, projectSwitch.SetEnabled(context.Background(), true))
	projects, err = projectSwitch.Lookup(context.Background(), []peasantproject.Key{key})
	require.NoError(t, err)
	require.Contains(t, projects, key)
	require.Equal(t, hash, projects[key].Hash)
	require.NotEmpty(t, projects[key].Name)

	require.NoError(t, projectSwitch.SetEnabled(context.Background(), false))
	projects, err = projectSwitch.Lookup(context.Background(), []peasantproject.Key{key})
	require.NoError(t, err)
	require.Empty(t, projects, "off clears lookups")
}

// TestServeDefaultPeasantSourceRefusesAnIncompatibleDatabase pins the actionable
// failure: an existing default file whose shape is not Peasant's leaves the
// switch off.
func TestServeDefaultPeasantSourceRefusesAnIncompatibleDatabase(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	dir := filepath.Join(xdg, "peasant")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "peasant.db")
	db, err := sql.Open("sqlite", "file:"+path)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE unrelated (id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	projectSwitch, err := newPeasantProjectSource("", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = projectSwitch.Close() })

	err = projectSwitch.SetEnabled(context.Background(), true)
	require.Error(t, err)
	require.Contains(t, err.Error(), path)
	require.Contains(t, err.Error(), "not usable")
	require.False(t, projectSwitch.Status().Enabled, "an incompatible default leaves enrichment off")
}

// the switch off with a lazy default-DB opener, one flag starts it enabled with
// that source, and both flags are a configuration error.
func TestServePeasantProjectSource(t *testing.T) {
	t.Parallel()

	switchOff, err := newPeasantProjectSource("", "")
	require.NoError(t, err)
	require.NotNil(t, switchOff)
	t.Cleanup(func() { _ = switchOff.Close() })
	off := switchOff.Status()
	require.False(t, off.Enabled)
	require.Equal(t, peasantproject.SourceDisabled, off.Source)
	require.Empty(t, off.State)
	require.Contains(t, off.Message, "turn it on from the web console")

	missingDB := filepath.Join(t.TempDir(), "missing-peasant.db")
	explicitDB, err := newPeasantProjectSource(missingDB, "")
	require.NoError(t, err)
	require.NotNil(t, explicitDB)
	t.Cleanup(func() { _ = explicitDB.Close() })
	dbStatus := explicitDB.Status()
	require.True(t, dbStatus.Enabled, "an explicit --peasant-db starts enabled, preserving previous behavior")
	require.Equal(t, peasantproject.SourceDB, dbStatus.Source)
	require.Equal(t, peasantproject.StateUnavailable, dbStatus.State)

	explicitAPI, err := newPeasantProjectSource("", "http://127.0.0.1:8787")
	require.NoError(t, err)
	require.NotNil(t, explicitAPI)
	t.Cleanup(func() { _ = explicitAPI.Close() })
	apiStatus := explicitAPI.Status()
	require.True(t, apiStatus.Enabled)
	require.Equal(t, peasantproject.SourceAPI, apiStatus.Source)
	require.Equal(t, peasantproject.StateUnavailable, apiStatus.State, "a configured API source is unavailable until its first lookup")
	require.Contains(t, apiStatus.Message, "not been contacted")

	_, err = newPeasantProjectSource("/tmp/peasant.db", "http://127.0.0.1:8787")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--peasant-db")
	require.Contains(t, err.Error(), "--peasant-url")
	require.Contains(t, err.Error(), "not both")

	_, err = newPeasantProjectSource("", "not-a-url")
	require.Error(t, err)
}

// TestServeDefaultPeasantSourceIsLazyAndActionable pins the no-flag path: no
// Peasant access at start, and enabling without the conventional database
// returns an actionable error while the switch stays off.
func TestServeDefaultPeasantSourceIsLazyAndActionable(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)

	projectSwitch, err := newPeasantProjectSource("", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = projectSwitch.Close() })
	require.False(t, projectSwitch.Status().Enabled)

	projects, err := projectSwitch.Lookup(context.Background(), []peasantproject.Key{{Harness: "claude-code", Session: "session"}})
	require.NoError(t, err)
	require.Empty(t, projects, "lookup while off must not touch Peasant")

	err = projectSwitch.SetEnabled(context.Background(), true)
	require.Error(t, err)
	require.Contains(t, err.Error(), filepath.Join(xdg, "peasant", "peasant.db"))
	require.Contains(t, err.Error(), "no Peasant database was found")
	require.Contains(t, err.Error(), "--peasant-db")
	require.False(t, projectSwitch.Status().Enabled, "a failed enable leaves enrichment off")
}

// TestServeRejectsBothPeasantSources proves the mutual-exclusion check runs
// before the store is opened: the requested database path is never created.
func TestServeRejectsBothPeasantSources(t *testing.T) {
	previous := flagDBPath
	flagDBPath = filepath.Join(t.TempDir(), "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	cmd := &cobra.Command{}
	cmd.Flags().String("listen", "127.0.0.1:0", "")
	cmd.Flags().Duration("active-window", 15*time.Minute, "")
	cmd.Flags().Bool("backfill", false, "")
	cmd.Flags().String("peasant-db", "/tmp/peasant.db", "")
	cmd.Flags().String("peasant-url", "http://127.0.0.1:8787", "")
	cmd.Flags().String("web-dir", "", "")
	require.NoError(t, cmd.Flags().Set("peasant-db", "/tmp/peasant.db"))
	require.NoError(t, cmd.Flags().Set("peasant-url", "http://127.0.0.1:8787"))

	err := runServe(cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not both")
	_, statErr := os.Stat(flagDBPath)
	require.True(t, os.IsNotExist(statErr), "validation must run before the pasture store is opened")
}

// TestServePeasantFlagsAreOptInDefaults pins the flag defaults: an operator who
// never passes either flag gets the disabled integration.
func TestServePeasantFlagsAreOptInDefaults(t *testing.T) {
	for _, name := range []string{"peasant-db", "peasant-url"} {
		flag := serveCmd.Flags().Lookup(name)
		require.NotNil(t, flag, name)
		require.Empty(t, flag.DefValue, "%s must default to disabled", name)
	}
}
