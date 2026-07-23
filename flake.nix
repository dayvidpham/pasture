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

        version = "0.1.0";

        # Pure Go build — no CGo required (modernc.org/sqlite is pure Go)
        commonAttrs = {
          inherit version;
          src = ./.;
          # Module-proxy mode: Nix fetches go.mod deps into a fixed-output
          # derivation (no committed vendor/ dir — modernc.org/libc alone would
          # bloat the repo). Update this hash whenever go.mod/go.sum changes.
          vendorHash = "sha256-Vse1siJKK/sccEV52Y6S/op+6xpJz0ZF1COs95Rp2Ms=";

          env.CGO_ENABLED = "0";

          # modernc.org/sqlite requires no native deps; pure Go build
          nativeBuildInputs = [ ];

          # `go test ./...` includes internal/release, whose integration tests
          # shell out to real `git` (init/commit/tag). Provide it in the check
          # sandbox — otherwise those tests fail with "git: not found".
          # git: internal/release integration tests shell out to real git.
          # jq: the hooks/ git-discipline test execs the hook, which parses the
          #     PreToolUse event with jq.
          nativeCheckInputs = [ pkgs.git pkgs.jq ];

          doCheck = true;
          checkPhase = ''
            runHook preCheck
            # buildGoModule adds -trimpath (good for the shipped binary) but it
            # rewrites runtime.Caller() to module-relative paths, which breaks
            # tests that locate repo-root testdata (e.g. skills/protocol/figures)
            # via runtime.Caller. Strip -trimpath for the test run only.
            export GOFLAGS="''${GOFLAGS//-trimpath/}"
            go test ./...
            runHook postCheck
          '';
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
          inherit pastured;
          inherit pasture-release;
          inherit pasture;
        };
      }
    );
}
