// Package redact is the non-bypassable redaction pipeline for the
// event log (C-004 / NFR-003).
//
// The pipeline is the single ingress that mutates payload bytes before
// they reach the storage layer. Built-in matchers detect credential
// patterns (API keys, JWTs, AWS access keys, Bearer / Basic tokens,
// PEM blocks, generic password=/secret= query strings); operator
// policy can mark structured fields by JSON path. Matched substrings
// are replaced with a deterministic placeholder generated from BLAKE3-
// keyed-MAC over a salt resolved from secrets-keychain (FR-006).
//
// Policy refuses "off" — disabling the pipeline is rejected at load
// time with a typed error.
package redact
