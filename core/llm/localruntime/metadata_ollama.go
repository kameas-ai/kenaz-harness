package localruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func init() {
	RegisterFetcher(KindOllama, &ollamaFetcher{})
}

// ollamaFetcher fetches the model list from Ollama's /api/tags endpoint.
type ollamaFetcher struct{}

// ollamaTagsResponse is the JSON shape returned by GET /api/tags.
type ollamaTagsResponse struct {
	Models []ollamaModelEntry `json:"models"`
}

type ollamaModelEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Details struct {
		QuantizationLevel string `json:"quantization_level"`
		ParameterSize     string `json:"parameter_size"`
	} `json:"details"`
}

func (f *ollamaFetcher) FetchModels(ctx context.Context, info RuntimeInfo) ([]LocalRuntimeModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/tags", info.DefaultBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama /api/tags: status %d", resp.StatusCode)
	}

	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("ollama /api/tags: decode: %w", err)
	}

	models := make([]LocalRuntimeModel, 0, len(tags.Models))
	for _, m := range tags.Models {
		quant := m.Details.QuantizationLevel
		if quant == "" {
			quant = ParseQuantLevel(m.Name)
		}
		dn := m.Name
		models = append(models, LocalRuntimeModel{
			ID:             m.Name,
			DisplayName:    dn,
			SizeBytes:      m.Size,
			QuantLevel:     quant,
			ParameterCount: parseParamCount(m.Details.ParameterSize),
		})
	}
	return models, nil
}

// parseParamCount converts a string like "7B", "13B", "70B", "1.5B" to a
// float64 in billions. Returns 0 when not parseable.
func parseParamCount(s string) float64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	var multiplier float64 = 1
	if strings.HasSuffix(s, "B") {
		multiplier = 1
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "M") {
		multiplier = 0.001
		s = s[:len(s)-1]
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return 0
	}
	return v * multiplier
}
