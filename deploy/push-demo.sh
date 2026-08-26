#!/usr/bin/env bash
#
# push-demo.sh — put the current build on the public demo box.
#
# The demo is NOT the container in docker-compose.yml. It is a systemd service
# behind Caddy on a box that has no Docker at all, which is why nothing in the
# release flow touches it: goreleaser publishes artifacts and wrangler deploys
# the site, and the demo went a full day serving a build older than the feature
# it was meant to show. This script is how it gets updated.
#
# Usage:
#   deploy/push-demo.sh                 # build, upload, restart, verify
#   HOST=1.2.3.4 deploy/push-demo.sh    # a different box
#   deploy/push-demo.sh --rollback      # put the previous binary back
set -euo pipefail

HOST="${HOST:-167.99.157.117}"
USER="${USER_REMOTE:-root}"
BIN=/opt/whodar/bin/whodar
UNIT=whodar-demo.service
URL="${URL:-https://demo.whodar.dev}"
SSH=(ssh -o BatchMode=yes -o ConnectTimeout=15 "$USER@$HOST")

say() { printf '\n=== %s ===\n' "$*"; }
die() { printf 'push-demo: %s\n' "$*" >&2; exit 1; }

if [ "${1:-}" = "--rollback" ]; then
	say "Rolling back"
	"${SSH[@]}" "test -f $BIN.prev && mv $BIN.prev $BIN && systemctl restart $UNIT"
	sleep 12
	curl -fsS -o /dev/null "$URL/" && echo "  demo answers again"
	exit 0
fi

say "Checks"
[ -z "$(git status --porcelain -- cmd internal)" ] || die "commit your code changes first"
go build ./... || die "the tree does not build"
echo "  at $(git log -1 --format='%h %s')"

say "Building for the box"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /tmp/whodar-demo-linux .
echo "  $(stat -f%z /tmp/whodar-demo-linux 2>/dev/null || stat -c%s /tmp/whodar-demo-linux) bytes"

say "Installing"
scp -o BatchMode=yes -q /tmp/whodar-demo-linux "$USER@$HOST:$BIN.new"
"${SSH[@]}" "
set -e
chmod +x $BIN.new
# A binary that cannot even print help is not going to serve the demo.
$BIN.new demo --help >/dev/null 2>&1
cp -p $BIN $BIN.prev
mv $BIN.new $BIN
systemctl restart $UNIT
"
echo "  restarted, waiting for the company to rebuild"
sleep 15

say "Verifying"
"${SSH[@]}" "systemctl is-active $UNIT" | sed 's/^/  service: /'
code=$(curl -s -o /tmp/demo-check.html -w '%{http_code}' --max-time 30 "$URL/")
[ "$code" = "200" ] || die "demo answered $code; run deploy/push-demo.sh --rollback"
for marker in exp-spans exp-regions dir-sort; do
	grep -q "$marker" /tmp/demo-check.html || die "served page is missing $marker; consider --rollback"
done
echo "  demo is serving the current build"
