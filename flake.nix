{
  description = "Pasture — Go port of aura-protocol for multi-agent orchestration";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfree = false;
        };

        # Single source of truth for the release identity: the same file the
        # release workflow reads to decide the tag (.github/workflows/release.yml
        # detect job tags v<version> from .claude-plugin/plugin.json). Reading it
        # here means a Nix build can never claim a version the release flow would
        # not tag, and a tagged release provably reports its own tag.
        version = (builtins.fromJSON (builtins.readFile ./.claude-plugin/plugin.json)).version;

        # Pure Go build — no CGo required (modernc.org/sqlite is pure Go)
        commonAttrs = {
          inherit version;
          src = ./.;
          # Module-proxy mode: Nix fetches go.mod deps into a fixed-output
          # derivation (no committed vendor/ dir — modernc.org/libc alone would
          # bloat the repo). Update this hash whenever go.mod/go.sum changes.
          vendorHash = "sha256-wJ3xtt5E1yFoCOg8ZVYZLkcUsLkyiY5pYDO12XVDLTI=";

          env.CGO_ENABLED = "0";

          # Stamp the release identity into every main package that declares a
          # `version` var (cmd/pasture and cmd/pastured). The linker silently ignores
          # -X for a main package without that symbol, so this is safe for the
          # other binaries. Unstamped builds (plain `go build`) keep their
          # honest "devel" default.
          ldflags = [ "-X main.version=v${version}" ];

          # modernc.org/sqlite requires no native deps; pure Go build
          nativeBuildInputs = [ ];

          # The race check includes internal/release, whose integration tests
          # shell out to real `git` (init/commit/tag), hooks/ tests that parse
          # PreToolUse events with `jq`, and generated command-adapter tests
          # that execute Python transport scripts.
          nativeCheckInputs = [ pkgs.bun pkgs.git pkgs.jq pkgs.python3 ];

          # Package outputs are build-only; the race check below owns the test
          # wave so each package is not tested repeatedly by Nix.
          doCheck = false;
        };

        pastured = pkgs.buildGoModule (commonAttrs // {
          pname = "pastured";
          subPackages = [ "cmd/pastured" ];
        });

        pasture-release = pkgs.buildGoModule (commonAttrs // {
          pname = "pasture-release";
          subPackages = [ "cmd/pasture-release" ];
        });

        # The pasture CLI: local task management backed by Provenance.
        pasture = pkgs.buildGoModule (commonAttrs // {
          pname = "pasture";
          subPackages = [ "cmd/pasture" ];
        });

        # All three binaries in one derivation for convenience
        pasture-bundle = pkgs.buildGoModule (commonAttrs // {
          pname = "pasture-bundle";
          subPackages = [
            "cmd/pastured"
            "cmd/pasture-release"
            "cmd/pasture"
          ];
        });

        race-check = pkgs.buildGoModule (commonAttrs // {
          pname = "pasture-race";
          subPackages = [
            "cmd/pastured"
            "cmd/pasture-release"
            "cmd/pasture"
          ];
          env.CGO_ENABLED = "1";
          doCheck = true;
          checkPhase = ''
            runHook preCheck
            # buildGoModule adds -trimpath (good for the shipped binary) but it
            # rewrites runtime.Caller() to module-relative paths, which breaks
            # tests that locate repo-root testdata (e.g. skills/protocol/figures)
            # via runtime.Caller. Strip -trimpath for the test run only.
            export GOFLAGS="''${GOFLAGS//-trimpath/}"
            go test -race ./...
            runHook postCheck
          '';
        });

        # The installer integration suite drives the built production
        # `pasture` binary through its real CLI surface from unrelated
        # temporary roots, against an isolated Claude Code host stand-in built
        # from testdata. It needs cgo for -race and no network access.
        installer-check = pkgs.buildGoModule (commonAttrs // {
          pname = "pasture-installer-integration";
          subPackages = [ "cmd/pasture" ];
          env.CGO_ENABLED = "1";
          doCheck = true;
          checkPhase = ''
            runHook preCheck
            # See race-check: -trimpath breaks tests that locate repo-root
            # testdata through runtime.Caller and the module root.
            export GOFLAGS="''${GOFLAGS//-trimpath/}"
            go test -race -count=1 ./internal/install/...
            runHook postCheck
          '';
        });

        devShell = pkgs.mkShell {
          name = "pasture-dev";
          packages = with pkgs; [
            gnumake
            go_1_26
            gopls
            gotools
            go-tools
            delve
            golangci-lint
            bun
            sqlite
          ];
          shellHook = ''
            echo "Pasture dev shell (Go $(go version | cut -d' ' -f3))"
            export CGO_ENABLED=0
          '';
        };

      in
      {
        packages = {
          inherit pastured;
          inherit pasture-release;
          inherit pasture;
          inherit pasture-bundle;
          default = pasture-bundle;
        };

        devShells.default = devShell;

        # nix flake check runs builds
        checks = {
          race = race-check;
          cgo-disabled-build = pasture-bundle;
          installer = installer-check;
        };
      }
    );
}
