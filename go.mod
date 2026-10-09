module github.com/Soroban-CII/soroindex

// The go directive is the oldest Go this module supports. It is 1.26 because
// modernc.org/sqlite v1.60.1 (and its modernc.org/libc, golang.org/x/sys deps)
// declare go 1.26.0, the highest requirement in the dependency graph.
// The toolchain line is the exact compiler used for builds and CI.
go 1.26.0

toolchain go1.27.2

require (
	github.com/stellar/go-stellar-sdk v0.7.3
	modernc.org/sqlite v1.60.1
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.17.6 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/stellar/go-xdr v0.0.0-20260806060815-dc590f17552a // indirect
	golang.org/x/sys v0.48.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
