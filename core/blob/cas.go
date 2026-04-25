package blob

import (
	"context"
	"io"
)

type Ref struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	MIME   string `json:"mime,omitempty"`
}

type CAS interface {
	Put(ctx context.Context, r io.Reader, mime string) (Ref, error)
	PutBytes(ctx context.Context, b []byte, mime string) (Ref, error)
	Get(ctx context.Context, digest string) (io.ReadCloser, Ref, error)
	Has(ctx context.Context, digest string) (bool, error)
	Delete(ctx context.Context, digest string) error
}
