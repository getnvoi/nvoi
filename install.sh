#!/bin/sh
# nvoi installer — curl -fsSL https://get.nvoi.to | sh
set -e

BASE_URL="${NVOI_BASE_URL:-https://get.nvoi.to}"
BINARY="nvoi"

if [ -t 1 ]; then
  BOLD="\033[1m" DIM="\033[2m" GREEN="\033[32m" RED="\033[31m"
  YELLOW="\033[33m" CYAN="\033[36m" RESET="\033[0m"
else
  BOLD="" DIM="" GREEN="" RED="" YELLOW="" CYAN="" RESET=""
fi

info()  { printf "${BOLD}${CYAN}==>${RESET} %s\n" "$*"; }
ok()    { printf "${BOLD}${GREEN} ok${RESET} %s\n" "$*"; }
warn()  { printf "${BOLD}${YELLOW}warn${RESET} %s\n" "$*" >&2; }
err()   { printf "${BOLD}${RED}error${RESET} %s\n" "$*" >&2; exit 1; }

detect_os() {
  case "$(uname -s)" in
    Linux*)  echo "linux" ;;
    Darwin*) echo "darwin" ;;
    MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
    *) err "unsupported OS: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) err "unsupported architecture: $(uname -m)" ;;
  esac
}

resolve_version() {
  if [ -n "${NVOI_VERSION:-}" ]; then echo "$NVOI_VERSION"; return; fi
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "${BASE_URL}/latest" 2>/dev/null || err "could not fetch latest version"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "${BASE_URL}/latest" 2>/dev/null || err "could not fetch latest version"
  else
    err "neither curl nor wget found"
  fi
}

download() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then wget -q -O "$2" "$1"
  else err "neither curl nor wget found"; fi
}

pick_install_dir() {
  if [ -n "${NVOI_INSTALL_DIR:-}" ]; then echo "$NVOI_INSTALL_DIR"; return; fi
  if [ -w /usr/local/bin ] || (command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null); then
    echo "/usr/local/bin"
  else
    echo "${HOME}/.local/bin"
  fi
}

maybe_sudo() {
  if [ "$(id -u)" -eq 0 ]; then "$@"
  elif [ -w "$(dirname "$1")" ] 2>/dev/null; then "$@"
  elif command -v sudo >/dev/null 2>&1; then sudo "$@"
  else "$@"; fi
}

main() {
  printf "\n${BOLD}  nvoi installer${RESET}\n\n"
  OS=$(detect_os)
  ARCH=$(detect_arch)
  info "detected ${OS}/${ARCH}"

  VERSION=$(resolve_version)
  info "version ${VERSION}"

  EXT=""; [ "$OS" = "windows" ] && EXT=".exe"
  ARTIFACT="nvoi-${OS}-${ARCH}${EXT}"
  URL="${BASE_URL}/${VERSION}/${ARTIFACT}"

  info "downloading ${DIM}${URL}${RESET}"
  TMPDIR=$(mktemp -d)
  trap 'rm -rf "$TMPDIR"' EXIT
  download "$URL" "${TMPDIR}/${ARTIFACT}" || err "download failed"
  chmod +x "${TMPDIR}/${ARTIFACT}"

  if ! "${TMPDIR}/${ARTIFACT}" --help >/dev/null 2>&1; then
    err "binary failed to execute"
  fi
  ok "binary verified"

  INSTALL_DIR=$(pick_install_dir)
  info "installing to ${INSTALL_DIR}/${BINARY}${EXT}"
  mkdir -p "$INSTALL_DIR" 2>/dev/null || maybe_sudo mkdir -p "$INSTALL_DIR"
  maybe_sudo install -m 0755 "${TMPDIR}/${ARTIFACT}" "${INSTALL_DIR}/${BINARY}${EXT}"
  ok "installed"

  if command -v "$BINARY" >/dev/null 2>&1; then
    printf "\n${BOLD}${GREEN}  nvoi ${VERSION} is ready${RESET}\n"
    printf "  run ${CYAN}nvoi deploy${RESET} to get started\n\n"
  else
    printf "\n${BOLD}${GREEN}  nvoi ${VERSION} installed${RESET}\n"
    printf "  add ${INSTALL_DIR} to your PATH, then run ${CYAN}nvoi deploy${RESET}\n\n"
  fi
}

main
