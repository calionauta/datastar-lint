#!/usr/bin/env bash
# Keeps the Datastar attribute vocabulary honest against upstream.
#
# knownAttrs is authored in this repo (Datastar publishes no SKILL.md/llms.txt to
# vendor and pin), so nothing else notices when upstream adds an attribute: this
# tool would report it as unknown until a human hits it. This check diffs the
# list against https://data-star.dev/reference/attributes in BOTH directions.
#
#   upstream has, we don't -> the tool is behind (a false UNKNOWN_ATTR)
#   we have, upstream not  -> the map is stale (we claim a dropped attribute)
#
# Usage:
#   bin/check-datastar-drift.sh                 # live network
#   bin/check-datastar-drift.sh --advisory      # report, exit 0
#   bin/check-datastar-drift.sh --fixture page  # offline (saved page)
#
# Scheduled by .github/workflows/datastar-drift.yml, weekly and not per-push:
# the finding is "upstream moved", which no push causes.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
URL="${DATASTAR_REF_URL:-https://data-star.dev/reference/attributes}"

advisory=0
fixture=""
while [ $# -gt 0 ]; do
	case "$1" in
	--advisory) advisory=1 ;;
	--fixture)
		shift
		fixture="${1:?--fixture needs a path}"
		;;
	-h | --help)
		sed -n '2,20p' "$0"
		exit 0
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
	shift
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ -n "$fixture" ]; then
	cp "$fixture" "$tmp/page.html"
else
	curl -fsSL --max-time 20 "$URL" -o "$tmp/page.html" ||
		{ echo "cannot fetch $URL (use --fixture for an offline check)" >&2; exit 2; }
fi

# Core attribute anchors only: the reference page renders one <h3 id="data-...">
# per attribute. The Pro section (<h2 id="pro-attributes">) is deliberately
# excluded — Pro is a paid product this tool does not cover.
#
# Flatten first, then cut the TEXT at the marker: the article is served as one
# enormous line and a line-oriented filter (awk/grep -v) drops or keeps whole
# records, losing whatever shares the marker's line (data-text was the first
# casualty) and leaking the Pro h3s that fall on later lines.
tr '\n' ' ' <"$tmp/page.html" |
	sed -e 's|id="pro-attributes".*||' |
	grep -oE 'id="data-[a-z][a-z-]+"' |
	sed -E 's/^id="(.*)"$/\1/' | sort -u >"$tmp/upstream.txt" || true

if [ ! -s "$tmp/upstream.txt" ]; then
	echo "could not extract any attribute from $URL — page layout changed?" >&2
	exit 2
fi

if [ -n "${DATASTAR_LINT_BIN:-}" ]; then
	"$DATASTAR_LINT_BIN" -list-attributes | sort -u >"$tmp/linter.txt"
else
	(cd "$REPO_ROOT" && go run . -list-attributes) | sort -u >"$tmp/linter.txt"
fi

comm -23 "$tmp/upstream.txt" "$tmp/linter.txt" >"$tmp/missing.txt"
comm -13 "$tmp/upstream.txt" "$tmp/linter.txt" >"$tmp/stale.txt"

missing_n=$(wc -l <"$tmp/missing.txt")
stale_n=$(wc -l <"$tmp/stale.txt")

echo "attributes: upstream $(wc -l <"$tmp/upstream.txt"), linter $(wc -l <"$tmp/linter.txt")"
if [ "$missing_n" -eq 0 ] && [ "$stale_n" -eq 0 ]; then
	echo "no drift: the vocabulary matches $URL"
	exit 0
fi

if [ "$missing_n" -gt 0 ]; then
	echo
	echo "DRIFT — upstream lists attributes this tool reports as unknown:"
	sed 's/^/  /' "$tmp/missing.txt"
	echo "  add them to knownAttrs (attrs.go) and to listattrs_test.go"
fi
if [ "$stale_n" -gt 0 ]; then
	echo
	echo "DRIFT — this tool knows attributes upstream no longer lists:"
	sed 's/^/  /' "$tmp/stale.txt"
	echo "  remove them, or mark ThirdParty: true if another library owns them"
fi

[ "$advisory" -eq 1 ] && exit 0
exit 1
