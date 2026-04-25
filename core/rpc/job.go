package rpc

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core"
	"github.com/sigil-tech/kaneaz-harness/core/scheduler"
)

type JobAPI struct{ c *core.Core }

func NewJobAPI(c *core.Core) *JobAPI { return &JobAPI{c: c} }

func (j *JobAPI) Upsert(ctx context.Context, job scheduler.Job) error {
	if j.c.Scheduler == nil {
		return errors.New("job: scheduler not configured")
	}
	return j.c.Scheduler.Upsert(ctx, job)
}

func (j *JobAPI) Delete(ctx context.Context, id string) error {
	if j.c.Scheduler == nil {
		return errors.New("job: scheduler not configured")
	}
	return j.c.Scheduler.Delete(ctx, id)
}

func (j *JobAPI) Get(ctx context.Context, id string) (*scheduler.Job, error) {
	if j.c.Scheduler == nil {
		return nil, errors.New("job: scheduler not configured")
	}
	return j.c.Scheduler.Get(ctx, id)
}

func (j *JobAPI) List(ctx context.Context) ([]scheduler.Job, error) {
	if j.c.Scheduler == nil {
		return nil, errors.New("job: scheduler not configured")
	}
	return j.c.Scheduler.List(ctx)
}

func (j *JobAPI) RunNow(ctx context.Context, id string) error {
	if j.c.Scheduler == nil {
		return errors.New("job: scheduler not configured")
	}
	return j.c.Scheduler.RunNow(ctx, id)
}
