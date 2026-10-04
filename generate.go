// Package gojiberry has no importable code of its own; this file only
// hosts the go:generate directive that regenerates every *.xg.go from
// its *.xg source via the xgc transpiler.
package gojiberry

//go:generate go run ./cmd/xgc .
