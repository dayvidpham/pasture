package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the local session-to-epoch read API",
	Long: `Serve the loopback-only session and epoch API from the local pasture database.

Routes:
  GET  /api/v1/sessions
  GET  /api/v1/sessions/{harness}/{nativeSessionId}
  GET  /api/v1/sessions/{harness}/{nativeSessionId}/transcript
  POST /api/v1/sessions/{harness}/{nativeSessionId}/bind
  POST /api/v1/sessions/{harness}/{nativeSessionId}/unbind
  GET  /api/v1/epochs and /api/v1/epochs/{id}
  GET  /api/v1/inspect
  GET  /api/v1/ws
  GET  /openapi.json

The transcript route streams the durable transcript that "peasant export
sessions" produces. It runs the peasant executable from this process's PATH;
without peasant, transcript requests answer 503 and the session, epoch,
bind/unbind, inspect, WebSocket, and OpenAPI routes keep working.

Opening the store follows the ordinary auto-on-open path: a missing database
file is created and an existing older schema is migrated. serve never runs a
separate "pasture migrate", and it refuses non-loopback hosts.

The server starts from the existing session cache; pass --backfill to rebuild
historical session state from the lifecycle journal before serving.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runServe(cmd)
	},
}

func init() {
	serveCmd.Flags().String("listen", "127.0.0.1:0", "Loopback TCP address to listen on")
	serveCmd.Flags().Duration("active-window", 15*time.Minute, "Age after which an open session is idle")
	serveCmd.Flags().Bool("backfill", false, "Rebuild historical session state before serving (can be slow on a large journal)")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command) error {
	listen, _ := cmd.Flags().GetString("listen")
	activeWindow, _ := cmd.Flags().GetDuration("active-window")
	backfill, _ := cmd.Flags().GetBool("backfill")
	if activeWindow <= 0 {
		return fmt.Errorf("active window must be positive")
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
	handler := sessionapi.NewHandler(service, store)

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
