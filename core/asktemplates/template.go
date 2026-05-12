// Package asktemplates resolves reusable elicitation templates for the
// kaneaz__ask_user_question builtin tool
// (ask-user-question-interactive-01KZNP3G WP07).
//
// Templates live in two locations:
//   - Bundled: embedded via //go:embed, path core/asktemplates/bundled/*.yaml.
//   - User:    <DataDir>/ask_templates/<name>.yaml (loaded at runtime).
//
// A user template with the same id as a bundled template shadows the bundled
// one; this lets users customise the ship-defaults without forking the binary.
//
// Template YAML schema mirrors the runtime question schema with {{var}}
// substitution in any string field.  Template-level metadata declares the
// expected vars so Resolve can reject callers that omit required ones.
package asktemplates

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// ─── Schema ──────────────────────────────────────────────────────────────────

// VarDef describes a single substitution variable declared by a template.
type VarDef struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type,omitempty"` // "string" | "int" | "bool" | "list"
	Required    bool   `yaml:"required"`
	Description string `yaml:"description,omitempty"`
}

// TemplateOption is one selectable entry in a question defined by a template.
type TemplateOption struct {
	Value string `yaml:"value"`
	Label string `yaml:"label"`
}

// TemplateQuestion is a single question inside a template.  String fields may
// contain {{var}} placeholders that Resolve substitutes with the caller's vars.
type TemplateQuestion struct {
	ID          string           `yaml:"id"`
	Kind        string           `yaml:"kind"`
	Prompt      string           `yaml:"prompt"`
	Placeholder string           `yaml:"placeholder,omitempty"`
	Options     []TemplateOption `yaml:"options,omitempty"`
	Min         *float64         `yaml:"min,omitempty"`
	Max         *float64         `yaml:"max,omitempty"`
	Step        *float64         `yaml:"step,omitempty"`
	Required    *bool            `yaml:"required,omitempty"`
	Multiline   bool             `yaml:"multiline,omitempty"`
	Destructive bool             `yaml:"destructive,omitempty"`
	Affirmative string           `yaml:"affirmative,omitempty"`
	Negative    string           `yaml:"negative,omitempty"`
}

// AskTemplate is the parsed representation of a template YAML file.
type AskTemplate struct {
	// ID is the canonical name used in tool invocations.  Derived from the
	// filename (minus .yaml) if not explicitly set.
	ID          string             `yaml:"id"`
	Description string             `yaml:"description,omitempty"`
	Vars        []VarDef           `yaml:"vars,omitempty"`
	Questions   []TemplateQuestion `yaml:"questions"`
}

// ─── Resolved shapes (returned to callers) ───────────────────────────────────

// ResolvedOption is an TemplateOption after variable substitution.
type ResolvedOption struct {
	Value string
	Label string
}

// ResolvedQuestion is a TemplateQuestion after variable substitution.
type ResolvedQuestion struct {
	ID          string
	Kind        string
	Prompt      string
	Placeholder string
	Options     []ResolvedOption
	Min         *float64
	Max         *float64
	Step        *float64
	Required    bool
	Multiline   bool
	Destructive bool
	Affirmative string
	Negative    string
}

// ─── Loader ──────────────────────────────────────────────────────────────────

// Loader holds bundled + user templates and can resolve them by name.
// Safe for concurrent use after construction.
type Loader struct {
	// bundledFS is the embedded filesystem populated by bundled.go.
	bundledFS fs.ReadFileFS
	// userDir is the optional path to <DataDir>/ask_templates.
	// Empty means no user templates are loaded.
	userDir string
}

// NewLoader constructs a Loader.
// bundledFS must implement fs.ReadFileFS; pass BundledFS (from bundled.go).
// userDir is the directory for user-defined YAML templates; empty = none.
func NewLoader(bundledFS fs.ReadFileFS, userDir string) *Loader {
	return &Loader{bundledFS: bundledFS, userDir: userDir}
}

// List returns the names (IDs) of all available templates.
// User templates shadow bundled ones with the same id.
func (l *Loader) List() ([]string, error) {
	index := map[string]bool{}

	// Bundled first — use the DirFS approach for the embed.FS.
	if l.bundledFS != nil {
		if rder, ok := l.bundledFS.(fs.ReadDirFS); ok {
			entries, err := rder.ReadDir(".")
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("asktemplates: list bundled: %w", err)
			}
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
					id := strings.TrimSuffix(e.Name(), ".yaml")
					index[id] = true
				}
			}
		}
	}

	// User templates (shadow bundled).
	if l.userDir != "" {
		entries, err := os.ReadDir(l.userDir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("asktemplates: list user: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
				id := strings.TrimSuffix(e.Name(), ".yaml")
				index[id] = true
			}
		}
	}

	names := make([]string, 0, len(index))
	for id := range index {
		names = append(names, id)
	}
	return names, nil
}

// load reads and parses a single template by name.  User templates shadow
// bundled ones.
func (l *Loader) load(name string) (*AskTemplate, error) {
	filename := name + ".yaml"

	// User templates take priority.
	if l.userDir != "" {
		path := filepath.Join(l.userDir, filename)
		data, err := os.ReadFile(path)
		if err == nil {
			return parseTemplate(name, data)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("asktemplates: read user %q: %w", name, err)
		}
	}

	// Bundled fallback.
	if l.bundledFS != nil {
		data, err := l.bundledFS.ReadFile(filename)
		if err == nil {
			return parseTemplate(name, data)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("asktemplates: read bundled %q: %w", name, err)
		}
	}

	return nil, fmt.Errorf("asktemplates: template %q not found", name)
}

func parseTemplate(name string, data []byte) (*AskTemplate, error) {
	var t AskTemplate
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("asktemplates: parse %q: %w", name, err)
	}
	// Derive id from filename when not set explicitly.
	if t.ID == "" {
		t.ID = name
	}
	if len(t.Questions) == 0 {
		return nil, fmt.Errorf("asktemplates: template %q has no questions", name)
	}
	return &t, nil
}

// Resolve loads the named template, validates that all required vars are
// provided, substitutes vars into every string field, and returns the
// resolved question list.
func (l *Loader) Resolve(name string, vars map[string]any) ([]ResolvedQuestion, error) {
	tmpl, err := l.load(name)
	if err != nil {
		return nil, err
	}

	// Validate required vars.
	for _, v := range tmpl.Vars {
		if !v.Required {
			continue
		}
		if _, ok := vars[v.Name]; !ok {
			return nil, fmt.Errorf("asktemplates: template %q requires var %q", name, v.Name)
		}
	}

	questions := make([]ResolvedQuestion, 0, len(tmpl.Questions))
	for i, q := range tmpl.Questions {
		rq, err := resolveQuestion(name, i, q, vars)
		if err != nil {
			return nil, err
		}
		questions = append(questions, rq)
	}
	return questions, nil
}

// resolveQuestion substitutes vars in all string fields of a TemplateQuestion.
func resolveQuestion(tmplName string, idx int, q TemplateQuestion, vars map[string]any) (ResolvedQuestion, error) {
	sub := func(s string) (string, error) {
		if !strings.Contains(s, "{{") {
			return s, nil
		}
		t, err := template.New("").Option("missingkey=zero").Parse(s)
		if err != nil {
			return "", fmt.Errorf("asktemplates: template %q q[%d] parse field: %w", tmplName, idx, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, vars); err != nil {
			return "", fmt.Errorf("asktemplates: template %q q[%d] execute: %w", tmplName, idx, err)
		}
		return buf.String(), nil
	}

	id, err := sub(q.ID)
	if err != nil {
		return ResolvedQuestion{}, err
	}
	prompt, err := sub(q.Prompt)
	if err != nil {
		return ResolvedQuestion{}, err
	}
	placeholder, err := sub(q.Placeholder)
	if err != nil {
		return ResolvedQuestion{}, err
	}
	affirmative, err := sub(q.Affirmative)
	if err != nil {
		return ResolvedQuestion{}, err
	}
	negative, err := sub(q.Negative)
	if err != nil {
		return ResolvedQuestion{}, err
	}

	opts := make([]ResolvedOption, 0, len(q.Options))
	for _, o := range q.Options {
		lbl, err := sub(o.Label)
		if err != nil {
			return ResolvedQuestion{}, err
		}
		val, err := sub(o.Value)
		if err != nil {
			return ResolvedQuestion{}, err
		}
		opts = append(opts, ResolvedOption{Value: val, Label: lbl})
	}

	required := true // default required
	if q.Required != nil {
		required = *q.Required
	}

	return ResolvedQuestion{
		ID:          id,
		Kind:        q.Kind,
		Prompt:      prompt,
		Placeholder: placeholder,
		Options:     opts,
		Min:         q.Min,
		Max:         q.Max,
		Step:        q.Step,
		Required:    required,
		Multiline:   q.Multiline,
		Destructive: q.Destructive,
		Affirmative: affirmative,
		Negative:    negative,
	}, nil
}
