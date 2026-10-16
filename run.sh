#!/usr/bin/env bash
# One-stop build/run script for macOS (works on Linux too).
#
#   ./run.sh            build, then print the full report in the terminal
#   ./run.sh html       build, run, and write + open report.html
#   ./run.sh ui         interactive dashboard in your web browser
#   ./run.sh app        build the double-clickable macOS app (dist/Baltimore Engine.app)
#   ./run.sh test       run unit + integration tests with the race detector
#   ./run.sh bench      time the ingest stage at 1, 2, 4 and 8 workers
#   ./run.sh fetch      re-download fresh raw data from data.baltimorecity.gov
#   ./run.sh unpack     gunzip the bundled snapshot (faster, fully parallel ingest)
#   ./run.sh build      just compile to ./bin/baltimore-engine
#   ./run.sh -- [flags] build, then run with custom flags (see --help)
set -euo pipefail
cd "$(dirname "$0")"

BIN=./bin/baltimore-engine

need_go() {
  if ! command -v go >/dev/null 2>&1; then
    cat >&2 <<'MSG'
Go is not installed (need Go 1.22 or newer).

Install it with ONE of:
  brew install go                                  # Homebrew
  open https://go.dev/dl/                          # official macOS .pkg installer

Then open a new terminal and re-run ./run.sh
MSG
    exit 1
  fi
}

build() {
  need_go
  echo "==> go build ($(go version | awk '{print $3}'))" >&2
  go build -o "$BIN" ./cmd/baltimore-engine
}

cmd="${1:-run}"
[[ $# -gt 0 ]] && shift
case "$cmd" in
  run)    build; "$BIN" run "$@" ;;
  ui)     build; "$BIN" ui "$@" ;;
  app)    need_go; ./scripts/build-app.sh && open -R "dist/Baltimore Engine.app" ;;
  html)   build; "$BIN" run -html report.html "$@" >/dev/null; command -v open >/dev/null && open report.html || echo "wrote report.html" ;;
  test)   need_go; go vet ./... && go test -race -count=1 ./... ;;
  bench)  build
          for w in 1 2 4 8; do
            printf 'workers=%s  ' "$w"
            "$BIN" run -workers "$w" -format plain "$@" 2>&1 >/dev/null | grep 'ingest'
          done ;;
  fetch)  build; "$BIN" fetch "$@" ;;
  unpack) for f in data/*.gz; do [[ -e "${f%.gz}" ]] || gunzip -k "$f"; done; ls -lh data ;;
  build)  build; echo "built $BIN" ;;
  --)     build; "$BIN" run "$@" ;;
  -h|--help|help) build; "$BIN" help; "$BIN" run -h ;;
  *)      echo "unknown command: $cmd (try: run | html | ui | app | test | bench | fetch | unpack | build)" >&2; exit 2 ;;
esac
