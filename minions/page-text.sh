#!/usr/bin/env bash
# usage: minions/page-text.sh PATH [MAX_CHARS=3000]   e.g. page-text.sh /items/169
# Print the visible text of a page of the running local service (scripts, styles and
# tags stripped, whitespace collapsed). Read-only GET. Address: NEWS_LIBRARY_ADDR.
set -euo pipefail
path=${1:?usage: page-text.sh PATH [MAX_CHARS]}
max=${2:-3000}
curl -sf "http://${NEWS_LIBRARY_ADDR:-127.0.0.1:8790}${path}" | perl -0777 -pe '
	s{<(script|style)\b.*?</\1>}{}gs; s{<[^>]+>}{ }g;
	s{&amp;}{&}g; s{&lt;}{<}g; s{&gt;}{>}g; s{&quot;}{"}g; s{&#39;|&#x27;}{\x27}g;
	s{\s+}{ }g' | cut -c1-"$max"
echo
