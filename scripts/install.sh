#!/bin/sh
# One-line installer for ainovel-cli
#
#   curl -fsSL https://raw.githubusercontent.com/voocel/ainovel-cli/main/scripts/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/voocel/ainovel-cli/v1.2.3/scripts/install.sh | sh -s -- v1.2.3
#
# Custom install directory: AINOVEL_INSTALL_DIR=~/.local/bin curl -fsSL ... | sh
# Specific version: AINOVEL_VERSION=v1.2.3 curl -fsSL ... | sh
set -e

REPO="voocel/ainovel-cli"
BIN="ainovel-cli"
DEST="${AINOVEL_INSTALL_DIR:-/usr/local/bin}"
VERSION="${AINOVEL_VERSION:-${1:-latest}}"

for cmd in curl tar; do
	command -v "$cmd" >/dev/null 2>&1 || { echo "Requires $cmd; please install it first and try again."; exit 1; }
done

if command -v sha256sum >/dev/null 2>&1; then
	sha256_file() { sha256sum "$1" | awk '{print tolower($1)}'; }
elif command -v shasum >/dev/null 2>&1; then
	sha256_file() { shasum -a 256 "$1" | awk '{print tolower($1)}'; }
else
	echo "sha256sum or shasum is required; install one and retry"
	exit 1
fi

curl_https() {
	curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 "$@"
}

case "$(uname -s)" in
	Darwin) OS="Darwin" ;;
	Linux)  OS="Linux" ;;
	*) echo "Unsupported system $(uname -s); on Windows, download manually from https://github.com/$REPO/releases"; exit 1 ;;
esac

case "$(uname -m)" in
	x86_64|amd64)  ARCH="x86_64" ;;
	arm64|aarch64) ARCH="arm64" ;;
	*) echo "Unsupported architecture $(uname -m)"; exit 1 ;;
esac

if [ "$VERSION" = "latest" ] || [ -z "$VERSION" ]; then
	API="https://api.github.com/repos/$REPO/releases/latest"
	echo "Checking the latest version..."
else
	case "$VERSION" in
		v*) TAG="$VERSION" ;;
		*) TAG="v$VERSION" ;;
	esac
	API="https://api.github.com/repos/$REPO/releases/tags/$TAG"
	echo "Checking version $TAG..."
fi

RELEASE=$(curl_https "$API")
TAG=$(printf '%s\n' "$RELEASE" | grep '"tag_name"' | head -1 | cut -d '"' -f 4)
[ -n "$TAG" ] || { echo "release lacks tag_name; refusing installation"; exit 1; }

RELEASE_VERSION=${TAG#v}
ASSET="${BIN}_${RELEASE_VERSION}_${OS}_${ARCH}.tar.gz"
CHECKSUMS="${BIN}_checksums.txt"
BASE_URL="https://github.com/$REPO/releases/download/$TAG"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Downloading $ASSET"
curl_https -o "$TMP/$ASSET" "$BASE_URL/$ASSET"
curl_https -o "$TMP/$CHECKSUMS" "$BASE_URL/$CHECKSUMS"

EXPECTED=$(awk -v asset="$ASSET" '
	NF == 2 {
		name = $2
		sub(/^\*/, "", name)
		if (name == asset) {
			count++
			sum = tolower($1)
		}
	}
	END {
		if (count != 1) exit 1
		print sum
	}
' "$TMP/$CHECKSUMS") || { echo "checksum manifest does not uniquely identify $ASSET; refusing installation"; exit 1; }

case "$EXPECTED" in
	*[!0-9a-f]*|'') echo "invalid SHA256 format for $ASSET; refusing installation"; exit 1 ;;
esac
[ "${#EXPECTED}" -eq 64 ] || { echo "invalid SHA256 length for $ASSET; refusing installation"; exit 1; }

ACTUAL=$(sha256_file "$TMP/$ASSET")
[ "$ACTUAL" = "$EXPECTED" ] || {
	echo "SHA256 verification failed for $ASSET; refusing installation"
	exit 1
}
echo "SHA256 verification passed"

CONTENTS=$(tar -tzf "$TMP/$ASSET") || { echo "cannot read archive; refusing installation"; exit 1; }
BIN_COUNT=$(printf '%s\n' "$CONTENTS" | awk -v bin="$BIN" '$0 == bin { count++ } END { print count + 0 }')
[ "$BIN_COUNT" -eq 1 ] || { echo "archive does not uniquely contain $BIN; refusing installation"; exit 1; }

mkdir "$TMP/extract"
tar -xOzf "$TMP/$ASSET" "$BIN" > "$TMP/extract/$BIN" || {
	echo "cannot extract $BIN from archive; refusing installation"
	exit 1
}
[ -s "$TMP/extract/$BIN" ] || { echo "$BIN in archive is empty; refusing installation"; exit 1; }
chmod 0755 "$TMP/extract/$BIN"

echo "Installing to $DEST"
[ -d "$DEST" ] || mkdir -p "$DEST" 2>/dev/null || sudo mkdir -p "$DEST"
if [ -w "$DEST" ]; then
	mv "$TMP/extract/$BIN" "$DEST/$BIN"
else
	echo "Administrator permissions are required to write to $DEST"
	sudo mv "$TMP/extract/$BIN" "$DEST/$BIN"
fi

echo "✓ Installation complete: $DEST/$BIN"
[ -n "$TAG" ] && echo "Version: $TAG"
command -v "$BIN" >/dev/null 2>&1 || echo "Tip: $DEST is not in PATH; please add it"
echo "Run $BIN to get started"
