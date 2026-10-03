{ pkgs ? import <nixpkgs> { } }:

pkgs.mkShell {
  packages = [
    pkgs.go
    pkgs.ffmpeg
    pkgs.python3
  ];

  LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath [
    pkgs.stdenv.cc.cc
    pkgs.zlib
  ];

  shellHook = ''
    echo "arabic-vocab: go $(go version | cut -d' ' -f3), $(python3 --version), $(ffmpeg -version | head -1 | cut -d' ' -f1-3)"
  '';
}
