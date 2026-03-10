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
          # null = use vendor directory; set to a sha256 for module proxy mode
          vendorHash = null;

          CGO_ENABLED = "0";

          # modernc.org/sqlite requires no native deps; pure Go build
          nativeBuildInputs = [];

          doCheck = true;
          checkPhase = ''
            runHook preCheck
            go test ./...
            runHook postCheck
          '';
        };

        pastured = pkgs.buildGoModule (commonAttrs // {
          pname = "pastured";
          subPackages = [ "cmd/pastured" ];
        });

        pasture-msg = pkgs.buildGoModule (commonAttrs // {
          pname = "pasture-msg";
          subPackages = [ "cmd/pasture-msg" ];
        });

        pasture-release = pkgs.buildGoModule (commonAttrs // {
          pname = "pasture-release";
          subPackages = [ "cmd/pasture-release" ];
        });

        # All three binaries in one derivation for convenience
        pasture-all = pkgs.buildGoModule (commonAttrs // {
          pname = "pasture";
          subPackages = [
            "cmd/pastured"
            "cmd/pasture-msg"
            "cmd/pasture-release"
          ];
        });

        devShell = pkgs.mkShell {
          name = "pasture-dev";
          packages = with pkgs; [
            go_1_24
            gopls
            gotools
            go-tools
            delve
            golangci-lint
            sqlite
            temporal-cli
          ];
          shellHook = ''
            echo "Pasture dev shell (Go $(go version | cut -d' ' -f3))"
            export CGO_ENABLED=0
          '';
        };

      in {
        packages = {
          inherit pastured;
          inherit pasture-msg;
          inherit pasture-release;
          pasture = pasture-all;
          default = pasture-all;
        };

        devShells.default = devShell;

        # nix flake check runs builds
        checks = {
          inherit pastured;
          inherit pasture-msg;
          inherit pasture-release;
        };
      }
    );
}
