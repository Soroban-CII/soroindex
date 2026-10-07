# SEP-47 parser vectors

Each `NAME.txt` holds the values of a contract's `sep` meta entries, one entry
per line; every line, including the last, ends in `\n`. An empty line is an
entry whose value is the empty string.

`NAME.lenient.json` and `NAME.strict.json` are the expected `SEPClaims` from
`sepmeta.ParseSEP47` in each mode. Regenerate them after a deliberate change with:

    go test ./pkg/sepmeta -run TestVectors -update

then review the diff. A changed vector output means `sepmeta.ParserVersion`
must be bumped in the same commit.
