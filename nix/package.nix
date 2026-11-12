{
  perSystem = { inputs', lib, pkgs, self', ... }: {
    packages = {
      default = self'.packages.nix-mcp;
      nix-mcp = (pkgs.buildGoModule.override { go = inputs'.devtools.packages.go; }) rec {
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
        # go-sum: a11858830919f620720658bf0ce7327225617f90e32d826939e46392a5ac85da
        vendorHash = "sha256-LjL/Bvbxwv/v61qbzqYAzHFS281jUeTBEksKwKjeX34=";
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
