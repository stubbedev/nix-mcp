{
  description = "nix-mcp — low-footprint MCP server for the Nix ecosystem (Go)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };

        nix-mcp = pkgs.buildGoModule {
          pname = "nix-mcp";
          version = "0.0.2";
          src = ./.;
          # buildGoModule fetches Go deps through the module proxy and hashes
          # the resulting vendor tree; `vendorHash` pins that hash so the
          # sandboxed build is reproducible. Bump after any `go get` / `go mod
          # tidy` that changes go.sum — `nix build` prints the expected hash on
          # mismatch, or run `just sync-flake`.
          # go-sum: 1626c0e10ffff62372f8a96282f63d1fa7fd940e4b877b820785b87fa4e2361f
          vendorHash = "sha256-XYkmkCwkHyGJLPOd49QHSob1BQJSNn2UFfRkBj3BMK0=";
          subPackages = [ "." ];
          ldflags = [
            "-s"
            "-w"
            "-X github.com/stubbedev/nix-mcp/version.Version=0.0.2"
          ];
          doCheck = true;

          meta = with pkgs.lib; {
            description = "Low-footprint MCP server for nixpkgs, NixOS/home-manager/darwin options, flakes, FlakeHub, NixHub, the binary cache and store";
            homepage = "https://github.com/stubbedev/nix-mcp";
            license = licenses.mit;
            mainProgram = "nix-mcp";
            platforms = platforms.unix;
          };
        };
      in
      {
        packages = {
          default = nix-mcp;
          nix-mcp = nix-mcp;
        };

        apps.default = {
          type = "app";
          program = "${nix-mcp}/bin/nix-mcp";
          meta = nix-mcp.meta;
        };

        checks.build = nix-mcp;

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            just
            git
          ];
        };

        formatter = pkgs.nixpkgs-fmt;
      });
}
