package hooks

import (
	"context"
	"errors"
	"testing"

	corehooks "github.com/sigil-tech/kaneaz-harness/core/hooks"
)

type fakeRegistry struct {
	hooks []corehooks.Hook
}

func (f *fakeRegistry) List() []corehooks.Hook {
	out := make([]corehooks.Hook, len(f.hooks))
	copy(out, f.hooks)
	return out
}
func (f *fakeRegistry) Get(id string) (corehooks.Hook, error) {
	for _, h := range f.hooks {
		if h.ID == id {
			return h, nil
		}
	}
	return corehooks.Hook{}, corehooks.ErrHookNotFound
}
func (f *fakeRegistry) Add(h corehooks.Hook) error {
	for _, existing := range f.hooks {
		if existing.ID == h.ID {
			return corehooks.ErrHookExists
		}
	}
	f.hooks = append(f.hooks, h)
	return nil
}
func (f *fakeRegistry) Update(h corehooks.Hook) error {
	for i := range f.hooks {
		if f.hooks[i].ID == h.ID {
			f.hooks[i] = h
			return nil
		}
	}
	return corehooks.ErrHookNotFound
}
func (f *fakeRegistry) Remove(id string) error {
	for i := range f.hooks {
		if f.hooks[i].ID == id {
			f.hooks = append(f.hooks[:i], f.hooks[i+1:]...)
			return nil
		}
	}
	return corehooks.ErrHookNotFound
}

type fakeBuiltins struct{ desc []corehooks.BuiltinDescriptor }

func (f *fakeBuiltins) Builtins() []corehooks.BuiltinDescriptor { return f.desc }

func TestAPI_AddListGetRemove(t *testing.T) {
	t.Parallel()
	a := New(Config{Registry: &fakeRegistry{}})
	ctx := context.Background()

	created, err := a.Add(ctx, HookInput{
		ID: "hk-1", Name: "test",
		Event: corehooks.EventPreSend, Kind: corehooks.KindBuiltin,
		Enabled: true, Builtin: corehooks.BuiltinMemoryRetrieve,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if created.Name != "test" {
		t.Errorf("created.Name = %q", created.Name)
	}
	list, _ := a.List(ctx)
	if len(list) != 1 {
		t.Fatalf("List len=%d, want 1", len(list))
	}
	got, err := a.Get(ctx, "hk-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "hk-1" {
		t.Errorf("Get.ID = %q", got.ID)
	}
	if err := a.Remove(ctx, "hk-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := a.Get(ctx, "hk-1"); !errors.Is(err, corehooks.ErrHookNotFound) {
		t.Errorf("post-remove Get: want ErrHookNotFound, got %v", err)
	}
}

func TestAPI_InstallStarterMemoryHooks_Idempotent(t *testing.T) {
	t.Parallel()
	a := New(Config{Registry: &fakeRegistry{}})
	ctx := context.Background()
	if err := a.InstallStarterMemoryHooks(ctx); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := a.InstallStarterMemoryHooks(ctx); err != nil {
		t.Fatalf("install (second): %v", err)
	}
	list, _ := a.List(ctx)
	if len(list) != 2 {
		t.Fatalf("len=%d, want 2", len(list))
	}
}

func TestAPI_RemoveStarterMemoryHooks(t *testing.T) {
	t.Parallel()
	a := New(Config{Registry: &fakeRegistry{}})
	ctx := context.Background()
	if err := a.InstallStarterMemoryHooks(ctx); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := a.RemoveStarterMemoryHooks(ctx); err != nil {
		t.Fatalf("remove: %v", err)
	}
	list, _ := a.List(ctx)
	if len(list) != 0 {
		t.Fatalf("len=%d, want 0", len(list))
	}
	// Removing again is a no-op.
	if err := a.RemoveStarterMemoryHooks(ctx); err != nil {
		t.Fatalf("remove (second): %v", err)
	}
}

func TestAPI_AvailableBuiltins(t *testing.T) {
	t.Parallel()
	a := New(Config{
		Registry: &fakeRegistry{},
		Builtins: &fakeBuiltins{desc: []corehooks.BuiltinDescriptor{
			{ID: "memory.retrieve", Name: "Memory: retrieve",
				Events: []string{corehooks.EventPreSend}},
		}},
	})
	descs, err := a.AvailableBuiltins(context.Background())
	if err != nil {
		t.Fatalf("AvailableBuiltins: %v", err)
	}
	if len(descs) != 1 || descs[0].ID != "memory.retrieve" {
		t.Fatalf("descs=%+v", descs)
	}
}

func TestAPI_NilRegistryGracefulErrors(t *testing.T) {
	t.Parallel()
	a := New(Config{})
	ctx := context.Background()
	list, err := a.List(ctx)
	if err != nil || len(list) != 0 {
		t.Errorf("List on nil reg: list=%+v err=%v", list, err)
	}
	if _, err := a.Get(ctx, "x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get: want ErrUnavailable, got %v", err)
	}
	if err := a.Remove(ctx, "x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Remove: want ErrUnavailable, got %v", err)
	}
}
