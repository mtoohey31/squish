{
  description = "squish";

  inputs = {
    nixpkgs.url = "nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }: {
    overlays.default = final: _: {
      squish = final.callPackage
        ({ buildGoModule }: buildGoModule {
          pname = "squish";
          version = "0.1.0";
          src = builtins.path { path = ./..; name = "squish-src"; };
          vendorHash = null;
        })
        { };
    };
  } // flake-utils.lib.eachDefaultSystem (system:
    let
      pkgs = import nixpkgs {
        overlays = [ self.overlays.default ];
        inherit system;
      };
      inherit (pkgs) gopls mkShell squish;
    in
    {
      packages.default = squish;

      devShells.default = mkShell {
        inputsFrom = [ squish ];
        packages = [ gopls ];
      };
    });
}
