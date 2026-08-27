#!/bin/sh
# Sign and notarize a macOS binary with quill, when the Apple material exists.
#
# goreleaser calls this for every built binary. Non-darwin binaries pass
# through untouched, and so does everything else until the three Apple secrets
# are configured, so a release without them is byte-identical to one before
# this hook existed. With them set, Gatekeeper accepts the shipped binary with
# no quarantine dance: signed with the Developer ID certificate, notarized by
# Apple, verified offline by cosign as before.
#
# Requires, all or nothing:
#   QUILL_SIGN_P12        the Developer ID Application .p12: a path, or base64
#   QUILL_SIGN_PASSWORD   password for the .p12
#   QUILL_NOTARY_KEY      the App Store Connect API key .p8: a path, or base64
#   QUILL_NOTARY_KEY_ID   the API key id
#   QUILL_NOTARY_ISSUER   the API issuer id
set -eu

path="$1"
os="$2"

[ "$os" = "darwin" ] || exit 0
if [ -z "${QUILL_SIGN_P12:-}" ] || [ -z "${QUILL_NOTARY_KEY:-}" ]; then
	echo "sign-darwin: no Apple signing material, shipping unsigned: $path" >&2
	exit 0
fi

QUILL_VERSION="v0.5.1"
if ! command -v quill >/dev/null 2>&1; then
	echo "sign-darwin: installing quill $QUILL_VERSION" >&2
	curl -sSfL https://get.anchore.io/quill | sh -s -- -b /usr/local/bin "$QUILL_VERSION"
fi

quill sign-and-notarize "$path"
