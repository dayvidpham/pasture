package promotion_test

import (
	"errors"
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/promotion"
)

func TestRunGatesRunsAllOnSuccess(t *testing.T) {
	var order []string
	mk := func(name string) promotion.Gate {
		g, err := promotion.NewFuncGate(name, func() error {
			order = append(order, name)
			return nil
		})
		if err != nil {
			t.Fatalf("gate %s: %v", name, err)
		}
		return g
	}
	if err := promotion.RunGates([]promotion.Gate{mk("a"), mk("b"), mk("c")}); err != nil {
		t.Fatalf("RunGates: %v", err)
	}
	if len(order) != 3 || order[0] != "a" || order[2] != "c" {
		t.Fatalf("gate order = %v, want [a b c]", order)
	}
}

func TestRunGatesStopsAtFirstFailure(t *testing.T) {
	var ran []string
	mk := func(name string, fail bool) promotion.Gate {
		g, err := promotion.NewFuncGate(name, func() error {
			ran = append(ran, name)
			if fail {
				return errors.New(name + " failed")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("gate %s: %v", name, err)
		}
		return g
	}
	err := promotion.RunGates([]promotion.Gate{mk("a", false), mk("b", true), mk("c", false)})
	if err == nil {
		t.Fatal("expected failure")
	}
	if len(ran) != 2 {
		t.Fatalf("ran = %v, want [a b] (c must not run after b fails)", ran)
	}
	var pe *promotion.Error
	if !errors.As(err, &pe) {
		t.Fatalf("error is not a promotion.Error: %v", err)
	}
}

func TestRunGatesRejectsNilGate(t *testing.T) {
	if err := promotion.RunGates([]promotion.Gate{nil}); err == nil {
		t.Fatal("expected error for nil gate")
	}
}

func TestCommandGatePassesOnZeroExit(t *testing.T) {
	dir, _ := effects.NewRepositoryID("/work")
	var gotDir, gotExe string
	var gotArgs []string
	runner := func(d, exe string, args ...string) (string, error) {
		gotDir, gotExe, gotArgs = d, exe, args
		return "ok", nil
	}
	resolver := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	g, err := promotion.NewCommandGate("pkg-tests", dir, "go", []string{"test", "./..."}, resolver, runner)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if err := g.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotDir != "/work" || gotExe != "/usr/bin/go" || len(gotArgs) != 2 {
		t.Fatalf("dispatched %q %q %v", gotDir, gotExe, gotArgs)
	}
}

func TestCommandGateFailsOnNonZeroExit(t *testing.T) {
	dir, _ := effects.NewRepositoryID("/work")
	runner := func(d, exe string, args ...string) (string, error) {
		return "", errors.New("exit 1")
	}
	resolver := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	g, err := promotion.NewCommandGate("pkg-tests", dir, "go", []string{"test", "./..."}, resolver, runner)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if err := g.Run(); err == nil {
		t.Fatal("expected non-zero exit to fail the gate")
	}
}

func TestCommandGateFailsWhenExecutableUnresolved(t *testing.T) {
	dir, _ := effects.NewRepositoryID("/work")
	runner := func(d, exe string, args ...string) (string, error) { return "", nil }
	resolver := func(name string) (string, error) { return "", errors.New("not found") }
	g, err := promotion.NewCommandGate("pkg-tests", dir, "go", nil, resolver, runner)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if err := g.Run(); err == nil {
		t.Fatal("expected unresolved executable to fail the gate")
	}
}

func TestNewCommandGateValidation(t *testing.T) {
	dir, _ := effects.NewRepositoryID("/work")
	resolver := func(name string) (string, error) { return name, nil }
	runner := func(d, exe string, args ...string) (string, error) { return "", nil }
	if _, err := promotion.NewCommandGate("", dir, "go", nil, resolver, runner); err == nil {
		t.Error("expected empty name to be rejected")
	}
	if _, err := promotion.NewCommandGate("g", effects.RepositoryID{}, "go", nil, resolver, runner); err == nil {
		t.Error("expected invalid dir to be rejected")
	}
	if _, err := promotion.NewCommandGate("g", dir, "", nil, resolver, runner); err == nil {
		t.Error("expected empty executable to be rejected")
	}
	if _, err := promotion.NewCommandGate("g", dir, "go", nil, nil, runner); err == nil {
		t.Error("expected nil resolver to be rejected")
	}
}

func TestNewFuncGateRejectsNilCheck(t *testing.T) {
	if _, err := promotion.NewFuncGate("g", nil); err == nil {
		t.Fatal("expected nil check to be rejected")
	}
}
