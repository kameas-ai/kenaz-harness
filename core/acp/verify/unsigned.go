package verify

// This file is intentionally a small shim that re-affirms the
// UnsignedAcceptVerifier export. The implementation lives in verify.go;
// we keep this file in line with WP04 / plan §6.6 file naming so the
// signed-cards-trust mission has a clear seam to drop in.
//
// When the signed-cards mission lands it will deliver an alternative
// implementation (e.g., X509Verifier) under this package or a sibling.
// Consumers depend only on the acp.Verifier interface — no envelope or
// transport changes required.
