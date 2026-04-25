package rpc

import "github.com/sigil-tech/kaneaz-harness/core"

type API struct {
	Bundle  *BundleAPI
	Session *SessionAPI
	Job     *JobAPI
	Config  *ConfigAPI
}

func New(c *core.Core) *API {
	return &API{
		Bundle:  NewBundleAPI(c),
		Session: NewSessionAPI(c),
		Job:     NewJobAPI(c),
		Config:  NewConfigAPI(c),
	}
}

func (a *API) Bindings() []any {
	return []any{a.Bundle, a.Session, a.Job, a.Config}
}
