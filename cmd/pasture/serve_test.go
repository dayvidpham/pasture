package main

import (
	"bufio"
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
