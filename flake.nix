{
  description = "Reconciliation service development environment";

  inputs = {
    nixpkgs.url = "https://flakehub.com/f/NixOS/nixpkgs/0.2511";
    nixpkgs-unstable.url = "https://flakehub.com/f/NixOS/nixpkgs/0.1";

    go-tooling = {
      url = "github:NixOS/nixpkgs/8cec5ef1131078ea76689e84c362b0ed6a1fb6a1";
      flake = false;
    };

    nixpkgs-go.url = "https://flakehub.com/f/NixOS/nixpkgs/0.2605";

    goreleaser = {
      url = "github:goreleaser/nur/52282e9a2d7c6882e9e0e8675d593b928c89e323";
      flake = false;
    };
  };

  outputs = { self, nixpkgs, nixpkgs-unstable, go-tooling, nixpkgs-go, goreleaser }:
    let
      supportedSystems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      forEachSupportedSystem = f:
        nixpkgs.lib.genAttrs supportedSystems (system:
          let
            pkgs = import nixpkgs {
              inherit system;
              overlays = [ ];
              config.allowUnfreePredicate = pkg: builtins.elem (nixpkgs.lib.getName pkg) [
                "goreleaser-pro"
              ];
            };
            pkgs-unstable = import nixpkgs-unstable {
              inherit system;
            };
          in
          f { pkgs = pkgs; pkgs-unstable = pkgs-unstable; system = system; }
        );
    in
    {
      devShells = forEachSupportedSystem ({ pkgs, pkgs-unstable, system }:
        let
          go = go127;
          compilerPackages = import nixpkgs-go { inherit system; };
          go127 = compilerPackages.go_1_27;
          buildGo127Module = compilerPackages.buildGo127Module;
          golangci-lint = pkgs.callPackage "${go-tooling}/pkgs/by-name/go/golangci-lint/package.nix" {
            inherit buildGo127Module;
          };
          stablePackages = with pkgs; [
            ginkgo
            gotools
            just
          ];
          toolingPackages = [
            go
            golangci-lint
          ];
          otherPackages = [
            (pkgs.callPackage "${goreleaser}/pkgs/goreleaser-pro" { })
          ];
        in
        {
          default = pkgs.mkShell {
            GOTOOLCHAIN = "local";
            GOROOT = "${go}/share/go";
            packages = stablePackages ++ toolingPackages ++ otherPackages;
          };
        }
      );
    };
}
