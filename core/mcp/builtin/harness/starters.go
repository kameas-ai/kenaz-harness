package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Starter is one curated onboarding prompt — the system prompt the
// agent runs with plus a recommended provider/model/recipes payload
// the dialog renders next to the title.
//
// Mission harness-self-mcp-onboarding-01KQ8TDU WP09.
type Starter struct {
	ID                  string   `json:"id"`
	Title               string   `json:"title"`
	Description         string   `json:"description"`
	RecommendedProvider string   `json:"recommendedProvider,omitempty"`
	RecommendedModel    string   `json:"recommendedModel,omitempty"`
	RecommendedRecipes  []string `json:"recommendedRecipes,omitempty"`
	SystemPrompt        string   `json:"systemPrompt"`
	Source              string   `json:"source"` // "embedded" | "user"
}

// LoadStarters returns the merged starter list: embedded defaults overlaid
// by anything the user dropped in <dataDir>/onboarding/prompts/. User
// files with the same id replace the embedded entry (so power users can
// tweak the canonical "code" prompt without losing the slot).
//
// dataDir may be empty; when so, only the embedded set is returned.
func LoadStarters(dataDir string) ([]Starter, error) {
	out := make(map[string]Starter)

	if err := walkStarterFS(EmbeddedStarters, "onboarding", "embedded", out); err != nil {
		return nil, fmt.Errorf("harness: load embedded starters: %w", err)
	}

	if dataDir != "" {
		userDir := filepath.Join(dataDir, "onboarding", "prompts")
		if info, err := os.Stat(userDir); err == nil && info.IsDir() {
			userFS := os.DirFS(userDir)
			if err := walkStarterFS(userFS, ".", "user", out); err != nil {
				return nil, fmt.Errorf("harness: load user starters: %w", err)
			}
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("harness: stat user starters dir: %w", err)
		}
	}

	starters := make([]Starter, 0, len(out))
	for _, s := range out {
		starters = append(starters, s)
	}
	sort.Slice(starters, func(i, j int) bool {
		return starters[i].ID < starters[j].ID
	})
	return starters, nil
}

func walkStarterFS(fsys fs.FS, root, source string, out map[string]Starter) error {
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		raw, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		s, err := parseStarter(raw, source)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if s.ID == "" {
			// Use file basename as id fallback.
			base := filepath.Base(path)
			s.ID = strings.TrimSuffix(base, filepath.Ext(base))
		}
		out[s.ID] = s
		return nil
	})
}

// parseStarter is a tiny YAML-ish frontmatter parser. We only need
// scalar key/value pairs and a single recommended_recipes list, so we
// avoid pulling in a yaml dependency. The body after the trailing `---`
// becomes the SystemPrompt.
func parseStarter(raw []byte, source string) (Starter, error) {
	s := Starter{Source: source}
	text := string(raw)

	if !strings.HasPrefix(text, "---") {
		// No frontmatter — treat the whole file as the system prompt.
		s.SystemPrompt = strings.TrimSpace(text)
		return s, nil
	}

	// Drop opening ---.
	text = strings.TrimPrefix(text, "---")
	text = strings.TrimLeft(text, "\r\n")

	end := strings.Index(text, "\n---")
	if end < 0 {
		return s, fmt.Errorf("missing closing frontmatter ---")
	}
	front := text[:end]
	body := strings.TrimLeft(text[end+len("\n---"):], "\r\n")
	s.SystemPrompt = strings.TrimSpace(body)

	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "- ") {
			// belongs to recommended_recipes
			s.RecommendedRecipes = append(s.RecommendedRecipes, strings.TrimSpace(line[2:]))
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		val = strings.Trim(val, `"'`)
		switch key {
		case "id":
			s.ID = val
		case "title":
			s.Title = val
		case "description":
			s.Description = val
		case "recommended_provider":
			s.RecommendedProvider = val
		case "recommended_model":
			s.RecommendedModel = val
		case "recommended_recipes":
			// Inline empty list `[]` shorthand.
			if val == "[]" {
				s.RecommendedRecipes = nil
			}
		}
	}
	return s, nil
}
