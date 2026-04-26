package sqlite

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
)

// notImplementedVector is the v1 stub for storage.VectorStore. Vector
// support lands in a separate mission.
type notImplementedVector struct{}

func (notImplementedVector) OpenCollection(_ context.Context, _ storage.CollectionSpec) (storage.Collection, error) {
	return nil, storage.ErrNotImplemented
}

func (notImplementedVector) Backends() []storage.VectorBackend {
	return nil
}

// notImplementedBackup is the v1 stub for storage.BackupTaker.
type notImplementedBackup struct{}

func (notImplementedBackup) Take(_ context.Context, _ string) (storage.BackupArtifact, error) {
	return storage.BackupArtifact{}, storage.ErrNotImplemented
}

// notImplementedDiagnostics is the v1 stub for storage.Diagnostics.
type notImplementedDiagnostics struct{}

func (notImplementedDiagnostics) Verify(_ context.Context) (storage.VerifyReport, error) {
	return storage.VerifyReport{}, storage.ErrNotImplemented
}

func (notImplementedDiagnostics) Status(_ context.Context) (storage.StatusReport, error) {
	return storage.StatusReport{}, storage.ErrNotImplemented
}
