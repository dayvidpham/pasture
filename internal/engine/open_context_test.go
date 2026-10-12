package engine

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/stretchr/testify/require"
)

func TestEngineContextFirstConnection(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "first.db")
	holder, err := dbconn.OpenDefaultDB(path)
	require.NoError(t, err)
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	_, err = holder.Exec("CREATE TABLE holder(v); BEGIN IMMEDIATE; INSERT INTO holder VALUES(1)")
	require.NoError(t, err)
	defer holder.Exec("ROLLBACK")
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	e, err := New(ctx, Config{DBPath: path, ApplicationVersion: "context-test"})
	require.Nil(t, e)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	// A full race suite schedules many SQLite workers at once. This tolerance
	// covers delayed timer/worker scheduling, not another SQLite wait: the
	// shared-opener ctx delivery is independently AST-pinned below. Keep this
	// well below the 500 ms production busy tier and the 438 ms lost-budget
	// regression; the separate audit short-write proof is not relaxed here.
	const schedulingTolerance = 150 * time.Millisecond
	require.Less(t, time.Since(start), 25*time.Millisecond+schedulingTolerance)
}

func TestEngineContextFallbacks(t *testing.T) {
	const fixture testutil.FixtureName = "open_context"
	var cases []struct {
		Name           string `yaml:"name"`
		SkipMigrations bool   `yaml:"skip_migrations"`
	}
	testutil.LoadFixtures(t, fixture, &cases)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "fallback.db")
			trail, err := audit.NewSqliteAuditTrailContext(t.Context(), path)
			require.NoError(t, err)
			require.NoError(t, trail.Close())
			holder, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), path, timeouts.ProductionProfile())
			require.NoError(t, err)
			defer holder.Close()
			holder.SetMaxOpenConns(1)
			ctx, cancel := context.WithCancel(t.Context())
			entered := make(chan struct{})
			done := make(chan struct{})
			var built *Engine
			var buildErr error
			var lockErr error
			// Cleanup is installed before launch, and releases the real holder
			// only after the completion observation (including on a failed proof).
			t.Cleanup(func() {
				cancel()
				_, _ = holder.Exec("ROLLBACK")
				<-done
				if built != nil {
					_ = built.Shutdown(time.Second)
				}
			})
			go func() {
				defer close(done)
				built, buildErr = newEngine(ctx, Config{DBPath: path, ApplicationVersion: "context-test", SkipMigrations: tc.SkipMigrations}, func(_ *sql.DB) {
					_, lockErr = holder.Exec("BEGIN IMMEDIATE; UPDATE audit_schema_meta SET version=version")
					cancel()
					close(entered)
				})
			}()
			select {
			case <-entered:
			case <-done:
				select {
				case <-entered:
				default:
					t.Fatalf("engine did not reach the gated fallback boundary: %v", buildErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("engine stalled before the gated fallback boundary")
			}
			const schedulingTolerance = 30 * time.Millisecond
			select {
			case <-done:
			case <-time.After(timeouts.ProductionProfile().SQLiteBusy() + schedulingTolerance):
				_, _ = holder.Exec("ROLLBACK")
				<-done
				t.Fatal("cancelled fallback did not finish within one busy tier while holder stayed locked")
			}
			require.Nil(t, built)
			require.NoError(t, lockErr)
			require.ErrorIs(t, buildErr, context.Canceled)
			require.ErrorContains(t, buildErr, "forensic audit trail")
		})
	}
}

func TestEnginePublicConstructorPinsContextAndObserver(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "engine.go", nil, 0)
	require.NoError(t, err)
	var delegation *ast.CallExpr
	var sharedOpen *ast.CallExpr
	var projectionSetup *ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := call.Fun.(*ast.Ident); ok && (name.Name == "ensureProjectionTable" || name.Name == "ensureProjectionTableContext") {
			require.Nil(t, projectionSetup)
			projectionSetup = call
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == "dbconn" && (sel.Sel.Name == "OpenSharedDBWithProfile" || sel.Sel.Name == "OpenSharedDBContextWithProfile") {
			require.Nil(t, sharedOpen)
			sharedOpen = call
		}
		return true
	})
	require.NotNil(t, sharedOpen)
	require.Equal(t, "OpenSharedDBContextWithProfile", sharedOpen.Fun.(*ast.SelectorExpr).Sel.Name)
	require.Len(t, sharedOpen.Args, 3)
	require.Equal(t, "ctx", sharedOpen.Args[0].(*ast.Ident).Name)
	require.NotNil(t, projectionSetup)
	require.Equal(t, "ensureProjectionTableContext", projectionSetup.Fun.(*ast.Ident).Name)
	require.Len(t, projectionSetup.Args, 2)
	require.Equal(t, "ctx", projectionSetup.Args[0].(*ast.Ident).Name)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "New" {
			continue
		}
		require.Len(t, fn.Body.List, 1)
		ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
		require.True(t, ok)
		delegation, ok = ret.Results[0].(*ast.CallExpr)
		require.True(t, ok)
	}
	require.NotNil(t, delegation)
	require.Equal(t, "newEngine", delegation.Fun.(*ast.Ident).Name)
	require.Len(t, delegation.Args, 3)
	for i, want := range []string{"ctx", "cfg", "nil"} {
		require.Equal(t, want, delegation.Args[i].(*ast.Ident).Name)
	}
}
