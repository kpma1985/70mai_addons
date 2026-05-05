#!/usr/bin/env bash
# Einheitlicher Einstieg: ./main.sh build | start | stop | restart | status
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

PIDFILE="$SCRIPT_DIR/.installer.pid"
BIN="$SCRIPT_DIR/bin/x800-addon-installer"
LOG="${LOG:-$SCRIPT_DIR/installer.log}"
LISTEN="${LISTEN:-127.0.0.1:8765}"

usage() {
	echo "Usage: $0 build | start | stop | restart | status" >&2
	echo "  Env (start): LISTEN=127.0.0.1:8765  LOG=…  CONFIG=…/installer.json  API_TOKEN=…" >&2
	exit 1
}

build() {
	mkdir -p "$SCRIPT_DIR/bin"
	echo "go build …"
	go build -trimpath -ldflags="-s -w" -o "$BIN" .
	"$BIN" -version
	echo "OK: $BIN"
}

start() {
	if [[ ! -x "$BIN" ]]; then
		echo "Binary fehlt: $BIN — zuerst ./main.sh build ausführen." >&2
		exit 1
	fi

	if [[ -f "$PIDFILE" ]]; then
		old="$(cat "$PIDFILE" 2>/dev/null || true)"
		if [[ -n "${old:-}" ]] && kill -0 "$old" 2>/dev/null; then
			echo "Läuft bereits (PID $old) — http://${LISTEN//127.0.0.1/localhost}"
			exit 0
		fi
		rm -f "$PIDFILE"
	fi

	args=( -listen "$LISTEN" )
	[[ -n "${CONFIG:-}" ]] && args+=( -config "$CONFIG" )
	[[ -n "${API_TOKEN:-}" ]] && args+=( -api-token "$API_TOKEN" )

	nohup "$BIN" "${args[@]}" >>"$LOG" 2>&1 &
	echo $! >"$PIDFILE"
	echo "Gestartet PID $(cat "$PIDFILE")"
	echo "URL:   http://${LISTEN//127.0.0.1/localhost}"
	echo "Log:   $LOG"
}

stop() {
	if [[ ! -f "$PIDFILE" ]]; then
		echo "Nicht gestartet (keine $PIDFILE)"
		return 0
	fi

	pid="$(cat "$PIDFILE" 2>/dev/null || true)"
	if [[ -z "${pid:-}" ]]; then
		rm -f "$PIDFILE"
		return 0
	fi

	if kill -0 "$pid" 2>/dev/null; then
		kill -TERM "$pid" 2>/dev/null || true
		for _ in 1 2 3 4 5 6 7 8 9 10; do
			kill -0 "$pid" 2>/dev/null || break
			sleep 0.2
		done
		if kill -0 "$pid" 2>/dev/null; then
			kill -KILL "$pid" 2>/dev/null || true
		fi
		echo "Gestoppt (PID $pid)"
	else
		echo "Prozess $pid nicht aktiv — PID-Datei entfernt."
	fi
	rm -f "$PIDFILE"
}

status() {
	if [[ ! -f "$PIDFILE" ]]; then
		echo "Status: nicht gestartet (keine $PIDFILE)"
		[[ -x "$BIN" ]] && echo "Binary: $BIN" || echo "Binary: fehlt (./main.sh build)"
		return 0
	fi

	pid="$(cat "$PIDFILE" 2>/dev/null || true)"
	if [[ -n "${pid:-}" ]] && kill -0 "$pid" 2>/dev/null; then
		echo "Status: läuft (PID $pid)"
	else
		echo "Status: PID-Datei verwaist — bitte ./main.sh stop"
	fi
	[[ -x "$BIN" ]] && echo "Binary: $BIN"
}

cmd="${1:-}"
case "$cmd" in
	build) build ;;
	start) start ;;
	stop) stop ;;
	restart)
		stop
		sleep 0.3
		start
		;;
	status) status ;;
	""|-h|--help|help) usage ;;
	*) usage ;;
esac
