{
  description = "Reconciliation service development environment";

  inputs = {
    nixpkgs.url = "https://flakehub.com/f/NixOS/nixpkgs/0.1";
    nur.url = "github:nix-community/NUR";
  };

  outputs = { self, nixpkgs, nur }:
    let
      goVersion = 24;
      supportedSystems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forEachSupportedSystem = f: nixpkgs.lib.genAttrs supportedSystems (system: f {
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            nur.overlays.default
            (final: prev: {
              go = prev."go_1_${toString goVersion}";
            })
          ];
        };
      });
    in
    {
      devShells = forEachSupportedSystem ({ pkgs }: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gotools
            golangci-lint
            ginkgo
            nur.repos.goreleaser.goreleaser-pro
            just
          ];
        };
      });
    };
}
