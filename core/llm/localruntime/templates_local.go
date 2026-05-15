package localruntime

// LocalRuntimeTemplate is a minimal template descriptor for a local runtime.
// These entries will be merged into the custom-openai templates.yaml at
// merge time when the custom-openai-compatible-endpoint mission lands.
//
// The fields here mirror the expected ProviderTemplate shape from the
// custom-openai mission spec.
//
// (local-model-runtimes-01KQ8VMZ WP03 — merge-ready templates)
type LocalRuntimeTemplate struct {
	// ID is the template identifier (used by AddProvider's "fromTemplate" flow).
	ID string
	// Name is the display name shown in the provider picker.
	Name string
	// Kind is always "custom-openai" for local runtimes.
	Kind string
	// DefaultBaseURL is the localhost URL for the runtime.
	DefaultBaseURL string
	// Port is the default port.
	Port int
	// NoCredential indicates this template does not require an API key.
	NoCredential bool
}

// LocalRuntimeTemplates is the ordered list of per-runtime templates.
// At merge time these entries are written into the custom-openai templates.yaml.
var LocalRuntimeTemplates = []LocalRuntimeTemplate{
	{
		ID:             "ollama",
		Name:           "Ollama (local)",
		Kind:           "custom-openai",
		DefaultBaseURL: "http://localhost:11434",
		Port:           11434,
		NoCredential:   true,
	},
	{
		ID:             "llama-server",
		Name:           "llama-server (local)",
		Kind:           "custom-openai",
		DefaultBaseURL: "http://localhost:8080",
		Port:           8080,
		NoCredential:   true,
	},
	{
		ID:             "lm-studio",
		Name:           "LM Studio (local)",
		Kind:           "custom-openai",
		DefaultBaseURL: "http://localhost:1234",
		Port:           1234,
		NoCredential:   true,
	},
	{
		ID:             "jan",
		Name:           "Jan (local)",
		Kind:           "custom-openai",
		DefaultBaseURL: "http://localhost:1337",
		Port:           1337,
		NoCredential:   true,
	},
	{
		ID:             "gpt4all",
		Name:           "GPT4All (local)",
		Kind:           "custom-openai",
		DefaultBaseURL: "http://localhost:4891",
		Port:           4891,
		NoCredential:   true,
	},
}
