package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
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
