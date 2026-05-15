// Package localruntime provides detection, capability introspection, and
// auto-configuration helpers for locally-running LLM runtimes (Ollama,
// llama-server, LM Studio, Jan, GPT4All).
//
// The package is wired by local-model-runtimes-01KQ8VMZ. When the
// HARNESS_LOCAL_RUNTIMES environment variable is set to "0" the entire
// subsystem is disabled: DetectAll returns an empty slice and all
// metadata fetchers return empty results without error.
//
// (local-model-runtimes-01KQ8VMZ WP01)
package localruntime

import "time"

// RuntimeKind is the canonical identifier for a supported local runtime.
type RuntimeKind string

const (
	KindOllama      RuntimeKind = "ollama"
	KindLlamaServer RuntimeKind = "llama-server"
	KindLMStudio    RuntimeKind = "lm-studio"
	KindJan         RuntimeKind = "jan"
	KindGPT4All     RuntimeKind = "gpt4all"
)

// AllKinds is the ordered set of all supported runtime kinds.
var AllKinds = []RuntimeKind{
	KindOllama,
	KindLlamaServer,
	KindLMStudio,
	KindJan,
	KindGPT4All,
}

// DetectionMethod records how a runtime was found.
type DetectionMethod string

const (
	// DetectionBinary — the runtime binary was found on $PATH.
	DetectionBinary DetectionMethod = "binary"
	// DetectionPort — a port-listening check confirmed the runtime is running.
	DetectionPort DetectionMethod = "port"
	// DetectionBoth — binary on PATH and port-listening check both confirmed.
	DetectionBoth DetectionMethod = "both"
)

// RuntimeInfo is the full descriptor for one detected local runtime.
// A runtime may be installed (binary found) but not running (no open port).
type RuntimeInfo struct {
	// Kind is the canonical runtime identifier.
	Kind RuntimeKind `json:"kind"`
	// Name is the human-readable display name.
	Name string `json:"name"`
	// BinaryPath is the resolved binary path from $PATH lookup, or empty
	// when the binary was not found on PATH.
	BinaryPath string `json:"binaryPath,omitempty"`
	// DefaultBaseURL is the canonical localhost URL for this runtime.
	DefaultBaseURL string `json:"defaultBaseURL"`
	// Port is the default port for the runtime.
	Port int `json:"port"`
	// Running is true when the runtime's HTTP API is reachable on Port.
	Running bool `json:"running"`
	// Installed is true when the binary was found on PATH.
	Installed bool `json:"installed"`
	// Method records how the runtime was detected (for diagnostics).
	Method DetectionMethod `json:"method,omitempty"`
	// DetectedAt is when this snapshot was taken.
	DetectedAt time.Time `json:"detectedAt"`
}

// LocalRuntimeModel is a model entry returned by the runtime's API.
type LocalRuntimeModel struct {
	// ID is the model identifier (e.g. "llama3:8b", "mistral:7b-instruct").
	ID string `json:"id"`
	// DisplayName is a clean human-readable label.
	DisplayName string `json:"displayName"`
	// SizeBytes is the model file size in bytes (0 when unknown).
	SizeBytes int64 `json:"sizeBytes,omitempty"`
	// QuantLevel is the quantization label (e.g. "Q4_K_M", "Q8_0", "F16").
	// Empty string when unknown.
	QuantLevel string `json:"quantLevel,omitempty"`
	// ParameterCount is billions of parameters as a float (0 when unknown).
	ParameterCount float64 `json:"parameterCount,omitempty"`
}
