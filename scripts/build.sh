#!/usr/bin/env bash
#
# Build script for jed-personal-mcp
#
# Usage:
#   ./scripts/build.sh              # Build for current platform
#   ./scripts/build.sh linux        # Build for linux/amd64
#   ./scripts/build.sh darwin       # Build for darwin/arm64
#   ./scripts/build.sh windows      # Build for windows/amd64
#   ./scripts/build.sh all          # Build for all platforms
#
set -euo pipefail

# Resolve project root (parent of scripts/)
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${ROOT}/bin"
MODULE_NAME="$(basename "${ROOT}")"

# Supported platforms: "os/arch"
PLATFORMS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
  "windows/arm64"
)

# Map short names to platform lists
resolve_platforms() {
  case "${1:-current}" in
    linux)
      echo "linux/amd64 linux/arm64"
      ;;
    darwin|macos)
      echo "darwin/amd64 darwin/arm64"
      ;;
    windows)
      echo "windows/amd64 windows/arm64"
      ;;
    all)
      echo "${PLATFORMS[*]}"
      ;;
    current)
      # Detect current platform
      local os arch
      os="$(uname -s | tr '[:upper:]' '[:lower:]')"
      arch="$(uname -m)"
      case "${arch}" in
        x86_64|amd64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
      esac
      echo "${os}/${arch}"
      ;;
    *)
      # Assume it's already in "os/arch" format
      echo "${1}"
      ;;
  esac
}

build_one() {
  local platform="$1"
  local os="${platform%%/*}"
  local arch="${platform##*/}"

  local out_name="${MODULE_NAME}"
  if [[ "${os}" == "windows" ]]; then
    out_name="${MODULE_NAME}.exe"
  fi

  local out_file="${BIN_DIR}/${out_name}"
  if [[ "${platform}" != "current" ]]; then
    out_file="${BIN_DIR}/${MODULE_NAME}-${os}-${arch}"
    if [[ "${os}" == "windows" ]]; then
      out_file="${out_file}.exe"
    fi
  fi

  echo "==> Building ${os}/${arch} -> ${out_file}"
  (
    cd "${ROOT}"
    GOOS="${os}" GOARCH="${arch}" CGO_ENABLED=0 \
      go build -trimpath -ldflags="-s -w" -o "${out_file}" .
  )
}

main() {
  local target="${1:-current}"
  local platforms
  platforms="$(resolve_platforms "${target}")"

  mkdir -p "${BIN_DIR}"

  for platform in ${platforms}; do
    build_one "${platform}"
  done

  echo ""
  echo "Build complete. Binaries in: ${BIN_DIR}"
  ls -lh "${BIN_DIR}"
}

main "$@"
