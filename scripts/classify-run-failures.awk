# Reads a `postman collection run --output` summary YAML (<prefix>.collection-run-summary.yaml)
# and prints key=value lines for scripts/run-collections.sh:
#   known=<n>   failures matching the exempted one (see KNOWN_* below)
#   other=<n>   every other failure
#   failed=<n>  stats.tests.failed
#   errors=<n>  request, iteration and script errors (stats.*.errors)
# It is deliberately strict: a failure counts as "known" only when the request, the assertion name
# and the start of the message all match.
BEGIN {
  KNOWN_REQUEST = "bookings / Create a booking"
  KNOWN_TEST    = "Request multipart bodies and Idempotency-Key follow their specifications"
  KNOWN_MESSAGE = "Idempotency-Key must be a Structured Field String"
}
function clean(s) {            # trim and drop YAML quotes
  sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s)
  if (s ~ /^'.*'$/ || s ~ /^".*"$/) s = substr(s, 2, length(s) - 2)
  return s
}
function flush() {
  if (!n) return
  if (src == KNOWN_REQUEST && tst == KNOWN_TEST && index(msg, KNOWN_MESSAGE) == 1) known++; else other++
}
/^[^ #]/ { section = ($0 ~ /^stats:/) ? "stats" : (($0 ~ /^failures:/) ? "failures" : "") }
section == "stats" {
  if ($0 ~ /^  [A-Za-z]+:/) { group = $1; sub(/:$/, "", group) }
  if ($0 ~ /^    failed:/ && group == "tests") failed = $2
  if ($0 ~ /^    errors:/) errors += $2
}
section == "failures" {
  if ($0 ~ /^  - /) { flush(); n = 1; src = tst = msg = ""; part = "" }
  if ($0 ~ /^    source:/) part = "source"
  else if ($0 ~ /^    error:/) part = "error"
  else if ($0 ~ /^    [a-z]+:/) part = ""
  if (part == "source" && $0 ~ /^      name:/) { v = $0; sub(/^      name:/, "", v); src = clean(v) }
  if (part == "error" && $0 ~ /^      message:/) { v = $0; sub(/^      message:/, "", v); msg = clean(v) }
  if (part == "error" && $0 ~ /^      test:/) { v = $0; sub(/^      test:/, "", v); tst = clean(v) }
}
END {
  flush()
  printf "known=%d\nother=%d\nfailed=%d\nerrors=%d\n", known + 0, other + 0, failed + 0, errors + 0
}
