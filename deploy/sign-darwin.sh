#!/bin/sh
# Sign and notarize a macOS binary with quill, when the Apple material exists.
#
# goreleaser calls this for every built binary. Non-darwin binaries pass
# through untouched, and so does everything else until the Apple secrets are
# configured, so a release without them is byte-identical to one before this
# hook existed. Turning signing on cannot make a release worse either: a
# failure inside signing is reported loudly and the binary ships unsigned,
# because a release with no artifacts is worse than one with unsigned ones. With them set, Gatekeeper accepts the shipped binary with
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
quill_bin="$(command -v quill 2>/dev/null || true)"
if [ -z "$quill_bin" ] || [ ! -x "$quill_bin" ]; then
	# Install into a private directory under the temp area rather than
	# /usr/local/bin. A CI runner is not root, so the installer there writes a
	# file it cannot make executable and the hook dies with "Permission
	# denied" after the binaries are already built. A private directory also
	# means the two darwin builds, which goreleaser runs at the same moment,
	# cannot install over each other.
	bindir="${TMPDIR:-/tmp}/quill-$$"
	mkdir -p "$bindir"
	echo "sign-darwin: installing quill $QUILL_VERSION into $bindir" >&2
	curl -sSfL https://get.anchore.io/quill | sh -s -- -b "$bindir" "$QUILL_VERSION" >&2
	quill_bin="$bindir/quill"
	chmod +x "$quill_bin"
fi

# Invoked by path, so signing never depends on what happens to be on PATH.
#
# A signing failure must never take the release with it. The whole point of
# this hook is that turning signing on cannot make a release worse than it was
# without it, and a release that ships no binaries at all is far worse than one
# that ships unsigned binaries. Apple's notary service goes down, tokens
# expire, and quill can fail for reasons nobody here controls; none of those
# are reasons to leave users with nothing to install. So the failure is loud
# and the release continues.
#
# Loud matters: this is the only signal that a release went out unsigned, and
# it is why every release is verified against the published artifact rather
# than against this log.
if ! "$quill_bin" sign-and-notarize "$path" >&2; then
	echo "sign-darwin: ============================================" >&2
	echo "sign-darwin: SIGNING FAILED, SHIPPING UNSIGNED: $path" >&2
	echo "sign-darwin: The release continues on purpose. Verify the" >&2
	echo "sign-darwin: published artifact and re-cut once this is fixed." >&2
	echo "sign-darwin: ============================================" >&2
fi
exit 0
