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
          version = "0.0.1";
          src = ./.;
          # buildGoModule fetches Go deps through the module proxy and hashes
          # the resulting vendor tree; `vendorHash` pins that hash so the
          # sandboxed build is reproducible. Bump after any `go get` / `go mod
          # tidy` that changes go.sum — `nix build` prints the expected hash on
          # mismatch, or run `just sync-flake`.
          # go-sum: 4484769bd1c0344929a5ebc1c5614db344f4c32d6a2347851e831dd3b0fbf9f6
          vendorHash = "sha256-XoTQndqEXw6BLR34RphjUZDpQVgLqCJ+c+pTDHUADp4=";
          subPackages = [ "." ];
          ldflags = [
            "-s"
            "-w"
            "-X github.com/stubbedev/nix-mcp/version.Version=0.0.1"
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
