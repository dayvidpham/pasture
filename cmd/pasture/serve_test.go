package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
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
		"/api/v1/sessions",
		"/transcript",
		"peasant",
		"PATH",
		"503",
		"bind",
		"unbind",
		"/api/v1/epochs",
		"/api/v1/epochs/{id}/sessions",
		"/api/v1/inspect",
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
		"setting both is an",
		"never opens a Peasant database",
	} {
		require.Contains(t, help, want)
	}
	require.Equal(t, "Serve the local session and epoch API", serveCmd.Short,
		"the command writes as well as reads; the short line must not call it a read API")
	require.Contains(t, help, "session and epoch API")
	require.NotContains(t, help, "read API")
	require.NotContains(t, help, "never migrates the schema")
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
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not print its bound address")
	}
	require.True(t, strings.HasPrefix(line, "pasture serve listening on http://127.0.0.1:"), line)
	base := strings.TrimSpace(strings.TrimPrefix(line, "pasture serve listening on "))

	response, err := http.Get(base + "/api/v1/sessions")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down after context cancellation")
	}
}

// TestServePeasantProjectSource pins the opt-in source selection: empty flags
// are disabled, both flags are a configuration error, and each single flag
// builds the matching source without touching Peasant for the other.
func TestServePeasantProjectSource(t *testing.T) {
	t.Parallel()

	source, closeSource, err := newPeasantProjectSource("", "")
	require.NoError(t, err)
	require.Nil(t, source, "the disabled default must not construct a source")
	require.Nil(t, closeSource)

	_, _, err = newPeasantProjectSource("/tmp/peasant.db", "http://127.0.0.1:8787")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--peasant-db")
	require.Contains(t, err.Error(), "--peasant-url")
	require.Contains(t, err.Error(), "not both")

	missingDB := filepath.Join(t.TempDir(), "missing-peasant.db")
	source, closeSource, err = newPeasantProjectSource(missingDB, "")
	require.NoError(t, err, "a missing Peasant database is unavailable, not a startup failure")
	require.NotNil(t, source)
	status := source.Status()
	require.True(t, status.Enabled)
	require.Equal(t, "db", status.Source)
	require.Equal(t, "unavailable", string(status.State))
	require.NotNil(t, closeSource)
	require.NoError(t, closeSource())
	require.NoError(t, closeSource(), "closing the source twice is harmless")

	source, closeSource, err = newPeasantProjectSource("", "http://127.0.0.1:8787")
	require.NoError(t, err)
	require.NotNil(t, source)
	status = source.Status()
	require.True(t, status.Enabled)
	require.Equal(t, "api", status.Source)
	require.Equal(t, "ok", string(status.State))
	require.NotNil(t, closeSource)
	require.NoError(t, closeSource())

	_, _, err = newPeasantProjectSource("", "not-a-url")
	require.Error(t, err)
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
