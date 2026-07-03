// Package gojiberry has no importable code of its own; this file only
// hosts the go:generate directive that regenerates every *_gox.go from
// its *.gox source via the goxc transpiler.
package gojiberry

//go:generate go run ./cmd/goxc .
