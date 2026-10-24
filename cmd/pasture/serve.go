package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
  GET  /api/v1/projects
  GET  /api/v1/projects/{projectHash}
  POST /api/v1/projects/{projectHash}/bind
  POST /api/v1/projects/{projectHash}/unbind
  GET  /api/v1/inspect
  GET  /api/v1/ws
  GET  /openapi.json

The web console is embedded in this binary and served at / from the same
loopback origin as the API; it needs no Node process. The API remains at
/api/v1/* and /openapi.json. Pass --web-dir <dir> to serve an external console
build instead of the embedded bundle (a development override). The directory
must be a trusted Vite production build with index.html at its root; symlinks
anywhere under it are refused.

The transcript route streams the durable transcript that "peasant export
sessions" produces. It runs the peasant executable from this process's PATH;
without peasant, transcript requests answer 503 and the session, epoch,
bind/unbind, inspect, WebSocket, and OpenAPI routes keep working.

Opening the store follows the ordinary auto-on-open path: a missing database
file is created and an existing older schema is migrated. serve never runs a
separate "pasture migrate", and it refuses non-loopback hosts.

The server starts from the existing session cache; pass --backfill to rebuild
historical session state from the lifecycle journal before serving.

Peasant project enrichment is opt-in and disabled by default; the web console
can turn it on and off at runtime. With neither --peasant-db nor --peasant-url,
turning it on reads the conventional Peasant SQLite database
($XDG_DATA_HOME/peasant/peasant.db, else ~/.local/share/peasant/peasant.db);
nothing is opened while the toggle is off, and a missing or incompatible
database returns an actionable error while enrichment stays off. Pass
--peasant-db <path> to read Peasant's project mapping from a read-only SQLite
handle, or --peasant-url <url> to ask the Peasant HTTP API; setting both is an
error. Both flags start enrichment enabled, and the console toggle still turns
that source off and on. With --peasant-db the reported label is the canonical
remote label when the remote is recognizable, else the canonical cwd, else the
project hash; with --peasant-url it is Peasant's resolved project label.`,
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
	serveCmd.Flags().String("web-dir", "", "Serve a trusted Vite production build from this directory (no symlinks) instead of the embedded bundle")
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
	projectSwitch, err := newPeasantProjectSource(peasantDB, peasantURL)
	if err != nil {
		return err
	}
	defer func() { _ = projectSwitch.Close() }()
	if !listenIsLoopback(listen) {
		return fmt.Errorf("serve refuses non-loopback listen address %q", listen)
	}
	// Resolve and validate the console before opening or migrating the store, so
	// a bad --web-dir or an incomplete embedded bundle fails before any durable
	// state is touched and before --backfill can run.
	var assets fs.FS
	if strings.TrimSpace(webDir) != "" {
		assets, err = webui.AssetsFromDir(webDir)
	} else {
		assets, err = webui.Embedded()
	}
	if err != nil {
		return err
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
	service.ProjectSource = projectSwitch
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
	console, err := webui.New(apiHandler, assets)
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

// newPeasantProjectSource constructs the operator's runtime project source
// switch. Both flags are a configuration error. One flag starts the switch on
// with that explicit source (the previous behavior). Neither flag starts it off
// with a lazy opener for the conventional Peasant database, so serve never
// probes Peasant until the operator turns enrichment on.
func newPeasantProjectSource(dbPath, apiURL string) (*peasantproject.Switch, error) {
	dbPath = strings.TrimSpace(dbPath)
	apiURL = strings.TrimSpace(apiURL)
	if dbPath != "" && apiURL != "" {
		return nil, fmt.Errorf("serve accepts only one Peasant project source: pass --peasant-db or --peasant-url, not both")
	}
	switch {
	case dbPath != "":
		open := func(context.Context) (peasantproject.Source, error) {
			return peasantproject.NewCached(peasantproject.NewDBSource(dbPath)), nil
		}
		initial, err := open(context.Background())
		if err != nil {
			return nil, err
		}
		return peasantproject.NewSwitch(open, initial), nil
	case apiURL != "":
		open := func(context.Context) (peasantproject.Source, error) {
			raw, err := peasantproject.NewAPISource(apiURL)
			if err != nil {
				return nil, err
			}
			return peasantproject.NewCached(raw), nil
		}
		initial, err := open(context.Background())
		if err != nil {
			return nil, err
		}
		return peasantproject.NewSwitch(open, initial), nil
	default:
		return peasantproject.NewSwitch(func(context.Context) (peasantproject.Source, error) {
			return openDefaultPeasantProjectSource()
		}, nil), nil
	}
}

// openDefaultPeasantProjectSource discovers the conventional Peasant database
// on first enable. A missing or incompatible file is an actionable error and
// leaves enrichment off; serve never creates or migrates Peasant's store.
func openDefaultPeasantProjectSource() (peasantproject.Source, error) {
	path := peasantproject.DefaultDBPath()
	if path == "" {
		return nil, fmt.Errorf("the default Peasant database path could not be resolved; set XDG_DATA_HOME or HOME, or pass --peasant-db/--peasant-url — Peasant project enrichment stays off")
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no Peasant database was found at %q; start Peasant so it creates that file, or pass --peasant-db/--peasant-url — Peasant project enrichment stays off", path)
		}
		return nil, fmt.Errorf("the default Peasant database at %q could not be read: %w; check its permissions, or pass --peasant-db/--peasant-url — Peasant project enrichment stays off", path, err)
	}
	raw := peasantproject.NewDBSource(path)
	if status := raw.Status(); status.State != peasantproject.StateOK {
		if closer, ok := raw.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return nil, fmt.Errorf("the default Peasant database at %q is not usable: %s — start Peasant or pass --peasant-db/--peasant-url; Peasant project enrichment stays off", path, status.Message)
	}
	return peasantproject.NewCached(raw), nil
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
