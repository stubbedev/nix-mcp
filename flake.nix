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

        nix-mcp = pkgs.buildGo127Module {
          pname = "nix-mcp";
          version = "0.0.2";
          src = ./.;
          # buildGoModule fetches Go deps through the module proxy and hashes
          # the resulting vendor tree; `vendorHash` pins that hash so the
          # sandboxed build is reproducible. Bump after any `go get` / `go mod
          # tidy` that changes go.sum — `nix build` prints the expected hash on
          # mismatch, or run `just sync-flake`.
          # go-sum: 26b1dfc46abd0d553cebe54a60a4acd0186ea26e0bba4b3047f894fc596c81f9
          vendorHash = "sha256-A1aE1Dk1W+Okx+v9JWUuAD2GL48CswQVxJ7fAnteNp0=";
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
            go_1_27
            gopls
            golangci-lint
            just
            git
          ];
        };

        formatter = pkgs.nixpkgs-fmt;
      });
}
