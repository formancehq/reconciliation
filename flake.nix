{
  description = "Reconciliation service development environment";

  inputs = {
    nixpkgs.url = "https://flakehub.com/f/NixOS/nixpkgs/0.2511";
    nixpkgs-unstable.url = "https://flakehub.com/f/NixOS/nixpkgs/0.1";

    rust-overlay = {
      url = "github:oxalica/rust-overlay";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    nur = {
      url = "github:nix-community/NUR";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, nixpkgs-unstable, rust-overlay, nur }:
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
              overlays = [ nur.overlays.default rust-overlay.overlays.default ];
              config.allowUnfreePredicate = pkg: builtins.elem (nixpkgs.lib.getName pkg) [
                "goreleaser-pro"
              ];
            };
            pkgs-unstable = import nixpkgs-unstable {
              inherit system;
            };
            speakeasyVersion = "1.761.1";
            speakeasyPlatform = {
              x86_64-linux = "linux_amd64";
              aarch64-linux = "linux_arm64";
              x86_64-darwin = "darwin_amd64";
              aarch64-darwin = "darwin_arm64";
            }.${system};
            speakeasySHA256 = {
              x86_64-linux = "a6160fc65fa07d97c3fd37280aa8b25549d10842ade7f132fda0e41cc5595b72";
              aarch64-linux = "f63ab5e73750af3c1028bcdaf562292bd03d698369c9ddac07f53ce9cba71308";
              x86_64-darwin = "2f4005908a5ddc117475e11e9f4e7a3ef70b19a2320edb4d1185bbf6ca1c3ef3";
              aarch64-darwin = "1249e12a58c43699d57711c8e10f803cc80e1fe0abb4d8a81da94dea5d50ac80";
            }.${system};
            speakeasy = pkgs.stdenvNoCC.mkDerivation {
              pname = "speakeasy";
              version = speakeasyVersion;
              src = pkgs.fetchurl {
                url = "https://github.com/speakeasy-api/speakeasy/releases/download/v${speakeasyVersion}/speakeasy_${speakeasyPlatform}.zip";
                sha256 = speakeasySHA256;
              };
              nativeBuildInputs = [ pkgs.unzip ];
              unpackPhase = "unzip $src";
              installPhase = ''
                mkdir -p $out/bin
                install -m 0755 speakeasy $out/bin/speakeasy
              '';
            };
            componentTools = import ./nix/component-toolchain.nix { inherit pkgs; };
          in
          f { pkgs = pkgs; pkgs-unstable = pkgs-unstable; inherit componentTools speakeasy system; }
        );
    in
    {
      # The component authoring toolchain is a set of Rust builds. Keep it out
      # of the default development shell: making every Go CI job compile it
      # turns a crates.io rate limit into an unrelated red build. It is exposed
      # here so the heavier component gate can enter it explicitly.
      packages = forEachSupportedSystem ({ componentTools, ... }: {
        inherit (componentTools) componentize-go wasi-virt wasm-tools;
        wasm-opt = componentTools.binaryen;
      });

      devShells = forEachSupportedSystem ({ pkgs, pkgs-unstable, speakeasy, system, ... }:
        let
          stablePackages = with pkgs; [
            ginkgo
            go_1_26
            gotools
            just
          ];
          unstablePackages = with pkgs-unstable; [
            golangci-lint
          ];
          otherPackages = [
            pkgs.nur.repos.goreleaser.goreleaser-pro
            speakeasy
          ];
        in
        {
          default = pkgs.mkShell {
            packages = stablePackages ++ unstablePackages ++ otherPackages;
          };
        }
      );

      # Opt-in gate: it builds the isolated toolchain, so it must stay off the
      # default `nix develop` path that every Go CI job takes.
      checks = forEachSupportedSystem ({ pkgs, system, ... }: {
        component-toolchain-versions = pkgs.runCommand "reconciliation-component-toolchain-versions" {
          nativeBuildInputs = with self.packages.${system}; [
            componentize-go
            wasi-virt
            wasm-tools
            wasm-opt
          ];
        } ''
          test "$(componentize-go --version)" = "componentize-go 0.4.1"
          test "$(wasi-virt --version)" = "wasi-virt 0.2.0"
          test "$(wasm-tools --version)" = "wasm-tools 1.239.0"
          test "$(wasm-opt --version)" = "wasm-opt version 124"
          touch "$out"
        '';
      });
    };
}
