package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/peasantproject"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/webui"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the local session and epoch API and web console",
	Long: `Serve the loopback-only session and epoch API and the compiled web console
from the local pasture database.

Routes:
  GET  /                      the compiled web console
  GET  /api/v1/sessions
  GET  /api/v1/sessions/{harness}/{nativeSessionId}
  GET  /api/v1/sessions/{harness}/{nativeSessionId}/transcript
  POST /api/v1/sessions/{harness}/{nativeSessionId}/bind
  POST /api/v1/sessions/{harness}/{nativeSessionId}/unbind
  GET  /api/v1/epochs and /api/v1/epochs/{id}
  GET  /api/v1/epochs/{id}/sessions
  GET  /api/v1/inspect
  GET  /api/v1/ws
  GET  /openapi.json

The web console is embedded in this binary and served at / from the same
loopback origin as the API; it needs no Node process. The API remains at
/api/v1/* and /openapi.json. Pass --web-dir <dir> to serve an external console
build instead of the embedded bundle (a development override).

The transcript route streams the durable transcript that "peasant export
sessions" produces. It runs the peasant executable from this process's PATH;
without peasant, transcript requests answer 503 and the session, epoch,
bind/unbind, inspect, WebSocket, and OpenAPI routes keep working.

Opening the store follows the ordinary auto-on-open path: a missing database
file is created and an existing older schema is migrated. serve never runs a
separate "pasture migrate", and it refuses non-loopback hosts.

The server starts from the existing session cache; pass --backfill to rebuild
historical session state from the lifecycle journal before serving.

Peasant project enrichment is opt-in and disabled by default. Pass
--peasant-db <path> to read Peasant's project mapping from a read-only SQLite
handle, or --peasant-url <url> to ask the Peasant HTTP API; setting both is an
error. With --peasant-db the reported label is the canonical remote label when
the remote is recognizable, else the canonical cwd, else the project hash; with
--peasant-url it is Peasant's resolved project label. When disabled, serve
never opens a Peasant database and never contacts Peasant.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runServe(cmd)
	},
}

func init() {
	serveCmd.Flags().String("listen", "127.0.0.1:0", "Loopback TCP address to listen on")
	serveCmd.Flags().Duration("active-window", 15*time.Minute, "Age after which an open session is idle")
	serveCmd.Flags().Bool("backfill", false, "Rebuild historical session state before serving (can be slow on a large journal)")
	serveCmd.Flags().String("peasant-db", "", "Opt-in: read project labels from the Peasant SQLite database at this path (read-only)")
	serveCmd.Flags().String("peasant-url", "", "Opt-in: read project labels from the Peasant HTTP API at this base URL")
	serveCmd.Flags().String("web-dir", "", "Serve the built web console from this directory instead of the embedded bundle")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command) error {
	listen, _ := cmd.Flags().GetString("listen")
	activeWindow, _ := cmd.Flags().GetDuration("active-window")
	backfill, _ := cmd.Flags().GetBool("backfill")
	peasantDB, _ := cmd.Flags().GetString("peasant-db")
	peasantURL, _ := cmd.Flags().GetString("peasant-url")
	webDir, _ := cmd.Flags().GetString("web-dir")
	if activeWindow <= 0 {
		return fmt.Errorf("active window must be positive")
	}
	projectSource, closeProjectSource, err := newPeasantProjectSource(peasantDB, peasantURL)
	if err != nil {
		return err
	}
	if closeProjectSource != nil {
		defer func() { _ = closeProjectSource() }()
	}
	if !listenIsLoopback(listen) {
		return fmt.Errorf("serve refuses non-loopback listen address %q", listen)
	}
	dbPath := flagDBPath
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	// A first serve may rebuild the complete occurrence projection. Keep the
	// hook-facing tiers unchanged, but give that one bounded whole-operation
	// wait room for a large existing journal.
	baseProfile := timeouts.ProductionProfile()
	serveProfile, err := timeouts.New(
		timeouts.Production,
		baseProfile.SQLiteBusy(),
		baseProfile.Ingress(),
		baseProfile.StartSlice(),
		baseProfile.HookInvocation(),
		10*time.Minute,
	)
	if err != nil {
		return err
	}
	tracker, err := tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithTimeoutProfile(serveProfile))
	if err != nil {
		return err
	}
	defer tracker.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if backfill {
		if _, _, err := tasks.EnsureSessionState(ctx, tracker); err != nil {
			return err
		}
	}
	readDB, err := dbconn.OpenReadOnlyDB(dbPath)
	if err != nil {
		return err
	}
	defer readDB.Close()
	if err := tasks.CheckSchemaVersion(readDB, dbPath); err != nil {
		return err
	}

	service := sessionapi.NewService(readDB)
	service.ActiveWindow = activeWindow
	service.ProjectSource = projectSource
	if backfill {
		service.Ensure = func(ctx context.Context) error {
			_, _, err := tasks.EnsureSessionState(ctx, tracker)
			return err
		}
	}
	store, err := tasks.NewSessionAPIStore(tracker)
	if err != nil {
		return err
	}
	apiHandler := sessionapi.NewHandler(service, store)

	var console http.Handler
	if webDir != "" {
		console, err = webui.NewFromDir(apiHandler, webDir)
	} else {
		console, err = webui.NewEmbedded(apiHandler)
	}
	if err != nil {
		return err
	}
	// The combined handler is guarded once more at the outermost layer so the
	// static console shares the API's loopback-only Host/Origin contract.
	// NewHandler already guards the API inside; the duplicate check on API
	// requests is intentional and keeps one guard responsible for every byte
	// this server emits.
	handler := sessionapi.LoopbackOnly(console)

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", listen, err)
	}
	if !addrIsLoopback(listener.Addr()) {
		_ = listener.Close()
		return fmt.Errorf("listen address %s is not loopback", listener.Addr())
	}
	fmt.Fprintf(cmd.OutOrStdout(), "pasture serve listening on http://%s\n", listener.Addr().String())

	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	select {
	case <-signalCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		serveErr := <-errCh
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	}
}

// newPeasantProjectSource constructs the operator's opt-in project source. An
// empty pair means disabled: no source, no Peasant access. Both flags set is a
// configuration error rather than an implicit precedence.
func newPeasantProjectSource(dbPath, apiURL string) (peasantproject.Source, func() error, error) {
	dbPath = strings.TrimSpace(dbPath)
	apiURL = strings.TrimSpace(apiURL)
	if dbPath != "" && apiURL != "" {
		return nil, nil, fmt.Errorf("serve accepts only one Peasant project source: pass --peasant-db or --peasant-url, not both")
	}
	var raw peasantproject.Source
	switch {
	case dbPath != "":
		raw = peasantproject.NewDBSource(dbPath)
	case apiURL != "":
		source, err := peasantproject.NewAPISource(apiURL)
		if err != nil {
			return nil, nil, err
		}
		raw = source
	default:
		return nil, nil, nil
	}
	cached := peasantproject.NewCached(raw)
	return cached, cached.Close, nil
}

func listenIsLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	return loopbackHostname(host)
}

func addrIsLoopback(addr net.Addr) bool {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP != nil && tcp.IP.IsLoopback()
	}
	host, _, err := net.SplitHostPort(addr.String())
	return err == nil && loopbackHostname(host)
}

func loopbackHostname(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
