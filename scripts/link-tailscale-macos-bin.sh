#!/usr/bin/env bash
# Legt ~/bin/tailscale an. Auf macOS darf die App-CLI nicht per Symlink
# angesprochen werden (BundleIdentifier-Fehler) — deshalb ein Wrapper mit exec.
set -euo pipefail

SRC="/Applications/Tailscale.app/Contents/MacOS/Tailscale"
DST="${HOME}/bin/tailscale"

if [[ ! -x "$SRC" ]]; then
	echo "Fehler: $SRC nicht ausführbar (Tailscale.app installiert?)." >&2
	exit 1
fi

mkdir -p "${HOME}/bin"
rm -f "$DST"

cat >"$DST" <<EOF
#!/bin/sh
exec "$SRC" "\$@"
EOF
chmod +x "$DST"

echo "OK: $DST (Wrapper -> $SRC)"
echo "Stelle sicher, dass ~/bin im PATH steht, z. B. in ~/.zshrc:"
echo '  export PATH="$HOME/bin:$PATH"'
