package rpc

import (
	"sync"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	llmcap "github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// modelInfoLookup is the adapter capability that answers a model's
// metadata from the provider's live model list.
type modelInfoLookup interface {
	LookupModelInfo(modelID string) (corellm.ModelInfo, bool)
}

// adapterLookup is the registry capability that returns the adapter of a
// provider kind.
type adapterLookup interface {
	Adapter(kind string) corellm.ProviderAdapter
}

// windowCatalog is the curated catalog's context-window lookup.
type windowCatalog interface {
	ContextWindow(provider, model string) int
}

// modelWindows is the one context-window lookup the request path and the
// model picker share, so the window a tool-schema budget is capped
// against is the window the picker shows. Resolution order: the user's
// per-provider-kind override (Settings.ContextWindowOverrides), the
// provider adapter's live model list (ModelInfo.ContextWindow), the
// curated capability catalog; 0 is unknown.
type modelWindows struct {
	reg       corellm.Registry
	cat       windowCatalog
	overrides func() map[string]int
}

// ContextWindow returns the context window of model on provider kind.
func (w modelWindows) ContextWindow(kind, model string) int {
	if w.overrides != nil {
		if v := w.overrides()[kind]; v > 0 {
			return v
		}
	}
	if al, ok := w.reg.(adapterLookup); ok {
		if ad := al.Adapter(kind); ad != nil {
			if ml, ok := ad.(modelInfoLookup); ok {
				if mi, ok := ml.LookupModelInfo(model); ok && mi.ContextWindow > 0 {
					return mi.ContextWindow
				}
			}
		}
	}
	if w.cat != nil {
		return w.cat.ContextWindow(kind, model)
	}
	return 0
}

var (
	defaultWindowCatalogOnce sync.Once
	defaultWindowCatalog     windowCatalog
)

// loadWindowCatalog returns the default capability catalog, loaded once.
// A load failure is logged once and leaves the catalog out of the
// lookup.
func loadWindowCatalog() windowCatalog {
	defaultWindowCatalogOnce.Do(func() {
		cat, err := llmcap.LoadDefault()
		if err != nil {
			logging.L().Warn("rpc.model_windows.catalog_load_failed", "err", err.Error())
			return
		}
		defaultWindowCatalog = cat
	})
	return defaultWindowCatalog
}

// newModelWindows builds the shared lookup over reg, the default catalog
// and the user's overrides read from settingsImpl on every call (nil
// settings: no overrides).
func newModelWindows(reg corellm.Registry, settingsImpl *settings.API) modelWindows {
	w := modelWindows{reg: reg, cat: loadWindowCatalog()}
	if settingsImpl != nil {
		w.overrides = func() map[string]int {
			st := settingsImpl.Store()
			if st == nil {
				return nil
			}
			s, err := st.LoadAll()
			if err != nil {
				return nil
			}
			return s.ContextWindowOverrides
		}
	}
	return w
}
