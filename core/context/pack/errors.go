package pack

import (
	"errors"
	"fmt"
)

// ErrorCode is a typed, machine-readable validation outcome (per WP01
// acceptance criterion: "All validator failure modes return typed errors
// distinguishable by code, not string match").
type ErrorCode string

const (
	// Parser errors.
	CodeManifestNotFound  ErrorCode = "manifest_not_found"
	CodeManifestUnreadable ErrorCode = "manifest_unreadable"
	CodeManifestMalformed ErrorCode = "manifest_malformed"
	CodeEntryUnreadable   ErrorCode = "entry_unreadable"
	CodeEntryMalformed    ErrorCode = "entry_malformed"
	CodeFrontmatterMissing ErrorCode = "frontmatter_missing"
	CodeFrontmatterInvalid ErrorCode = "frontmatter_invalid"
	CodePathTraversal     ErrorCode = "path_traversal"

	// Validator errors.
	CodeRequiredFieldMissing ErrorCode = "required_field_missing"
	CodeInvalidLayer         ErrorCode = "invalid_layer"
	CodeInvalidEntryKind     ErrorCode = "invalid_entry_kind"
	CodeDuplicateEntryName   ErrorCode = "duplicate_entry_name"
	CodeSignatureRefMissing  ErrorCode = "signature_ref_missing"
	CodeOversizeLayer        ErrorCode = "oversize_layer"
	CodeOversizeEntry        ErrorCode = "oversize_entry"
	CodeInvalidName          ErrorCode = "invalid_name"
	CodeInvalidVersion       ErrorCode = "invalid_version"
)

// Error is the typed error returned by the parser and validator.
//
// Code is the only stable contract the caller may switch on. Subject
// names the offending pack, entry, or field. Cause carries the underlying
// IO/decode error if any.
type Error struct {
	Code    ErrorCode
	Subject string
	Message string
	Cause   error
}

// Error implements error.
func (e *Error) Error() string {
	parts := fmt.Sprintf("pack: %s", e.Code)
	if e.Subject != "" {
		parts = fmt.Sprintf("%s [%s]", parts, e.Subject)
	}
	if e.Message != "" {
		parts = fmt.Sprintf("%s: %s", parts, e.Message)
	}
	if e.Cause != nil {
		parts = fmt.Sprintf("%s: %v", parts, e.Cause)
	}
	return parts
}

// Unwrap exposes the underlying cause for errors.Is / errors.As.
func (e *Error) Unwrap() error { return e.Cause }

// Is allows callers to compare via errors.Is using a sentinel-by-code:
//
//	errors.Is(err, &pack.Error{Code: pack.CodeDuplicateEntryName})
//
// matches any pack.Error with that code regardless of subject / cause.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	if t.Code == "" {
		return true
	}
	return t.Code == e.Code
}

func newErr(code ErrorCode, subject, message string, cause error) *Error {
	return &Error{Code: code, Subject: subject, Message: message, Cause: cause}
}

// HasCode is a convenience for matching error codes from typed Errors.
func HasCode(err error, code ErrorCode) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == code
}
