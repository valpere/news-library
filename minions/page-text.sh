#!/usr/bin/env bash
# usage: minions/page-text.sh PATH [MAX_CHARS=3000]   e.g. page-text.sh /items/169
# Print the visible text of a page of the running local service (scripts, styles and
# tags stripped, entities decoded, whitespace collapsed, cut to MAX_CHARS characters).
# Read-only GET. Address: NEWS_LIBRARY_ADDR (default 127.0.0.1:8790).
set -euo pipefail
path=${1:?usage: page-text.sh PATH [MAX_CHARS]}
max=${2:-3000}
[[ $max =~ ^[0-9]+$ ]] || { echo "MAX_CHARS must be a number" >&2; exit 2; }
curl -sf "http://${NEWS_LIBRARY_ADDR:-127.0.0.1:8790}${path}" | MAX="$max" perl -CSD -0777 -ne '
	s{<(script|style)\b.*?</\1>}{}gsi; s{<[^>]+>}{ }g;
	s{&nbsp;}{ }g; s{&#(\d+);}{chr($1)}ge; s{&#x([0-9a-f]+);}{chr(hex($1))}gie;
	s{&lt;}{<}g; s{&gt;}{>}g; s{&quot;}{"}g; s{&amp;}{&}g;
	s{\s+}{ }g; print substr($_, 0, $ENV{MAX}), "\n"'
