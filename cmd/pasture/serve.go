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
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the local session-to-epoch read API",
	Long: `Serve the loopback-only session API from the local pasture database.

The server exposes GET /api/v1/sessions and
GET /api/v1/sessions/{harness}/{nativeSessionId}. It rebuilds the disposable
session cache from the lifecycle journal when the cache is dirty or behind, and
it never migrates the schema or accepts non-loopback hosts.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runServe(cmd)
	},
}

func init() {
	serveCmd.Flags().String("listen", "127.0.0.1:0", "Loopback TCP address to listen on")
	serveCmd.Flags().Duration("active-window", 15*time.Minute, "Age after which an open session is idle")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command) error {
	listen, _ := cmd.Flags().GetString("listen")
	activeWindow, _ := cmd.Flags().GetDuration("active-window")
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
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return err
	}
	defer tracker.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if _, _, err := tasks.EnsureSessionState(ctx, tracker); err != nil {
		return err
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
	service.Ensure = func(ctx context.Context) error {
		_, _, err := tasks.EnsureSessionState(ctx, tracker)
		return err
	}
	handler := sessionapi.NewHandler(service)

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
