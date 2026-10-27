#!/bin/sh
# Instalador de MKD.
#
#   curl -fsSL https://raw.githubusercontent.com/Yak036/mkd/main/install.sh | sh
#
# Variables:
#   MKD_VERSION      versión a instalar (por defecto: la última release)
#   MKD_INSTALL_DIR  directorio destino (por defecto: /usr/local/bin o ~/.local/bin)
#   MKD_REPO         repositorio origen (por defecto: Yak036/mkd)
#
# Instala el binario con permisos del usuario. Si el directorio destino no es
# escribible cae a ~/.local/bin sin usar sudo nunca de forma silenciosa.

set -eu

REPO="${MKD_REPO:-Yak036/mkd}"
VERSION="${MKD_VERSION:-}"
BINARY="mkd"

info()  { printf '\033[1;34m::\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
fail()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

need() {
	command -v "$1" >/dev/null 2>&1 || fail "falta '$1' en PATH"
}

need curl
need tar
need uname

# --- Plataforma --------------------------------------------------------------
os="$(uname -s)"
case "$os" in
	Linux|Darwin) ;;
	MINGW*|MSYS*|CYGWIN*|Windows_NT)
		printf '%s\n' "estás en Windows. Instala con PowerShell:"
		printf '%s\n' "  irm https://raw.githubusercontent.com/Yak036/mkd/main/install.ps1 | iex"
		exit 1
		;;
	*) fail "sistema no soportado: $os (descarga el binario desde GitHub Releases)" ;;
esac

arch="$(uname -m)"
case "$arch" in
	x86_64|amd64) arch="amd64" ;;
	aarch64|arm64) arch="arm64" ;;
	*) fail "arquitectura no soportada: $arch" ;;
esac

info "plataforma detectada: ${os}/${arch}"

# --- Versión -----------------------------------------------------------------
if [ -z "$VERSION" ]; then
	VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
		| sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
	[ -n "$VERSION" ] || fail "no se pudo determinar la última versión; define MKD_VERSION"
fi
VERSION="${VERSION#v}"
info "versión: v${VERSION}"

# --- Descarga ----------------------------------------------------------------
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT INT TERM

# goreleaser nombra los artefactos en minúsculas: mkd_linux_amd64.tar.gz
tarball="${BINARY}_$(printf '%s' "$os" | tr '[:upper:]' '[:lower:]')_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/v${VERSION}/${tarball}"

info "descargando ${url}"
curl -fsSL -o "${tmpdir}/${tarball}" "$url" \
	|| fail "descarga fallida (¿existe v${VERSION} para ${os}/${arch}?)"

tar -xzf "${tmpdir}/${tarball}" -C "$tmpdir" "$BINARY" \
	|| fail "el archivo no contiene el binario ${BINARY}"

# --- Destino -----------------------------------------------------------------
choose_dir() {
	if [ -n "${MKD_INSTALL_DIR:-}" ]; then
		echo "$MKD_INSTALL_DIR"
		return
	fi
	if [ -w /usr/local/bin ] 2>/dev/null; then
		echo /usr/local/bin
		return
	fi
	echo "${HOME}/.local/bin"
}

dir="$(choose_dir)"
mkdir -p "$dir" || fail "no se pudo crear $dir"

install -m 0755 "${tmpdir}/${BINARY}" "${dir}/${BINARY}" \
	|| fail "no se pudo instalar en $dir"

info "instalado en ${dir}/${BINARY}"

case ":${PATH}:" in
	*":${dir}:"*) ;;
	*)
		warn "${dir} no está en PATH. Añade esto a tu shell:"
		# SC2016: $PATH va literal para que el usuario lo copie tal cual.
		# shellcheck disable=SC2016
		printf '\n  export PATH="%s:$PATH"\n\n' "$dir"
		;;
esac

if "${dir}/${BINARY}" --version >/dev/null 2>&1; then
	info "$("${dir}/${BINARY}" --version)"
	info "listo. Configura tu proveedor con: ${BINARY} config set deepinfra --key sk-... --model ..."
else
	warn "el binario se instaló pero no responde a --version"
fi
