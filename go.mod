module github.com/Soroban-CII/soroindex

// The go directive is the oldest Go this module supports. It is 1.26 because
// modernc.org/sqlite v1.60.1 (and its modernc.org/libc, golang.org/x/sys deps)
// declare go 1.26.0, the highest requirement in the dependency graph.
// The toolchain line is the exact compiler used for builds and CI.
go 1.26.0

toolchain go1.27.1
