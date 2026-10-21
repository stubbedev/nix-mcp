{
  perSystem = { lib, pkgs, self', ... }: {
    packages = {
      default = self'.packages.nix-mcp;
      nix-mcp = pkgs.buildGo127Module rec {
        pname = "nix-mcp";
        version = "0.0.2";
        src = lib.fileset.toSource {
          root = ../.;
          fileset = lib.fileset.difference (lib.fileset.gitTracked ../.) (
            lib.fileset.unions [
              ../flake.nix
              ../flake.lock
              ../nix
            ]
          );
        };
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
          "-X github.com/stubbedev/nix-mcp/version.Version=${version}"
        ];
        doCheck = true;

        meta = {
          description = "Low-footprint MCP server for nixpkgs, NixOS/home-manager/darwin options, flakes, FlakeHub, NixHub, the binary cache and store";
          homepage = "https://github.com/stubbedev/nix-mcp";
          license = lib.licenses.mit;
          mainProgram = "nix-mcp";
          platforms = lib.platforms.unix;
        };
      };
    };

    apps.default = {
      type = "app";
      program = lib.getExe self'.packages.nix-mcp;
      inherit (self'.packages.nix-mcp) meta;
    };

    checks.build = self'.packages.nix-mcp;
  };
}
