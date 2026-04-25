package scheduler

import "context"

type Scheduler interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	Upsert(ctx context.Context, j Job) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*Job, error)
	List(ctx context.Context) ([]Job, error)

	RunNow(ctx context.Context, id string) error
	ReconcileMissed(ctx context.Context, since int64) error
}

type Store interface {
	Upsert(ctx context.Context, j Job) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*Job, error)
	List(ctx context.Context) ([]Job, error)
}
