package localruntime

import (
	"context"
	"testing"
)

// TestJanFetcher_MissingDirNoError verifies walkJanModels returns an empty
// slice (not an error) when the Jan models directory does not exist.
func TestJanFetcher_MissingDirNoError(t *testing.T) {
	// Set HOME to a temp dir that has no .jan/models subdirectory.
	t.Setenv("HOME", t.TempDir())

	models, err := walkJanModels(context.Background())
	if err != nil {
		t.Errorf("expected nil error for missing dir; got: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected empty slice; got %d models", len(models))
	}
}

// TestGPT4AllFetcher_MissingDirNoError verifies walkGPT4AllModels returns
// an empty slice when none of the candidate directories exist.
func TestGPT4AllFetcher_MissingDirNoError(t *testing.T) {
	// Set HOME to a temp dir with no GPT4All subdirectory.
	t.Setenv("HOME", t.TempDir())

	models, err := walkGPT4AllModels()
	if err != nil {
		t.Errorf("expected nil error for missing dir; got: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected empty slice; got %d models", len(models))
	}
}

// TestQuantLevel checks quantization parsing across common real-world labels.
func TestQuantLevel(t *testing.T) {
	cases := []struct {
		in   string
		want string // exact match or empty for "no quant"
	}{
		{"llama3:8b-instruct-q4_K_M", "Q4_K_M"},
		{"llama-3.1-8b-Q4_K_M.gguf", "Q4_K_M"},
		{"mistral-7b-instruct-v0.2.Q5_K_S.gguf", "Q5_K_S"},
		{"phi-2.Q8_0.gguf", "Q8_0"},
		{"model_F16.gguf", "F16"},
		{"codellama-7b-python.Q4_0.gguf", "Q4_0"},
		{"mistral:7b", ""},
		{"gpt2", ""},
	}
	for _, c := range cases {
		got := ParseQuantLevel(c.in)
		if c.want == "" {
			if got != "" {
				t.Errorf("ParseQuantLevel(%q) = %q; want empty", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("ParseQuantLevel(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
