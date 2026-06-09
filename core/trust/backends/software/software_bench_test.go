//go:build test_software_backend
// +build test_software_backend

package software_test

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/trust"
	"github.com/kameas-ai/kenaz-harness/core/trust/backends/software"
)

// BenchmarkSoftwareSign captures NFR-002 evidence: < 20 ms p95 for
// software-backend Sign on a developer laptop. Run:
//
//	go test -tags test_software_backend -bench=BenchmarkSoftwareSign \
//	    ./core/trust/backends/software/...
func BenchmarkSoftwareSign(b *testing.B) {
	bk := software.New()
	b.Cleanup(func() { _ = bk.Close() })
	if _, err := bk.GenerateAndStore("bench"); err != nil {
		b.Fatalf("GenerateAndStore: %v", err)
	}
	payload := make([]byte, 256)
	for i := range payload {
		payload[i] = byte(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := bk.Sign(context.Background(), trust.BackendRef{Path: "bench"}, trust.AlgEd25519, payload)
		if err != nil {
			b.Fatalf("Sign: %v", err)
		}
	}
}
