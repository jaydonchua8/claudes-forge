#!/bin/sh
# Cross-compiles the Windows build into windows/dist (works from macOS or Linux with Go installed).
set -e
cd "$(dirname "$0")"
mkdir -p dist
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o dist/claudes-forge.exe .
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o dist/forge-hook.exe ./hook
cd dist && rm -f claudes-forge-windows.zip && zip -q claudes-forge-windows.zip claudes-forge.exe forge-hook.exe
echo "built windows/dist/claudes-forge-windows.zip"
