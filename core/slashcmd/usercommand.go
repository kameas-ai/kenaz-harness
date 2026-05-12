package slashcmd

// UserCommandScope discriminates global (always-available) versus
// project-scoped (available only in sessions that belong to a
// specific project) user commands.
type UserCommandScope string

const (
	ScopeGlobal  UserCommandScope = "global"
	ScopeProject UserCommandScope = "project"
)

// UserCommandKind discriminates the three execution modes a user
// command may declare.
type UserCommandKind string

const (
	KindText   UserCommandKind = "text"   // literal text expansion
	KindTool   UserCommandKind = "tool"   // tool dispatch with arg template
	KindPrompt UserCommandKind = "prompt" // LLM prompt with template variables
)

// InputKind enumerates the per-input widget types rendered by the
// SlashArgFill chip strip.
type InputKind string

const (
	InputKindText        InputKind = "text"
	InputKindEnum        InputKind = "enum"
	InputKindNumber      InputKind = "number"
	InputKindFile        InputKind = "file"
	InputKindArtifactRef InputKind = "artifact_ref"
	InputKindProjectRef  InputKind = "project_ref"
)

// UserCommandInput is one declared input parameter on a user command.
// The frontend renders a per-type widget in the SlashArgFill chip strip.
type UserCommandInput struct {
	// Name is the parameter name used in template substitution.
	Name string `yaml:"name" json:"name"`
	// Kind is the input widget type.
	Kind InputKind `yaml:"type" json:"kind"`
	// Required, when true, blocks submission until a value is supplied.
	Required bool `yaml:"required" json:"required"`
	// EnumValues lists the allowed values when Kind is InputKindEnum.
	EnumValues []string `yaml:"enum_values,omitempty" json:"enumValues,omitempty"`
	// Default is the pre-filled value shown before the user edits.
	Default string `yaml:"default,omitempty" json:"default,omitempty"`
	// Hint is an optional inline hint rendered near the widget.
	Hint string `yaml:"hint,omitempty" json:"hint,omitempty"`
}

// UserCommand is the canonical in-memory representation of a
// user-defined slash command. It mirrors both the SQLite
// slash_commands_user row and the markdown-with-frontmatter file
// stored on disk.
//
// Cross-mission contract: the skills-catalog mission
// (model-invoked-skills-catalog-01KZNP3E) reads ModelInvokable via
// Registry.ListModelInvokable. That mission will add a follow-up
// migration (version 1001+) that adds the model_invokable column;
// until then ModelInvokable comes from the parsed markdown frontmatter.
type UserCommand struct {
	// Name is the bare command token (no leading slash).
	// Must match ^[a-z][a-z0-9_-]{0,30}$.
	Name string `yaml:"name" json:"name"`

	// Scope is "global" or "project".
	Scope UserCommandScope `yaml:"scope" json:"scope"`

	// ProjectID is required when Scope is "project". Null when global.
	ProjectID string `yaml:"project_id,omitempty" json:"projectId,omitempty"`

	// Kind is "text", "tool", or "prompt".
	Kind UserCommandKind `yaml:"kind" json:"kind"`

	// Description is a one-line summary rendered in autocomplete and /help.
	Description string `yaml:"description" json:"description"`

	// WhenToUse is a prose hint for the model-invoked path
	// (consumed by model-invoked-skills-catalog-01KZNP3E).
	WhenToUse string `yaml:"when_to_use,omitempty" json:"whenToUse,omitempty"`

	// DoesNotHandle documents cases this command explicitly rejects.
	// Used by the skills catalog to build the negative examples list.
	DoesNotHandle string `yaml:"does_not_handle,omitempty" json:"doesNotHandle,omitempty"`

	// ModelInvokable, when true, makes this command appear in the
	// model-invokable catalog (skills-catalog mission).
	// The skills-catalog mission adds the persistent DB column via
	// migration 1001; this field is sourced from the markdown frontmatter
	// until that migration lands.
	ModelInvokable bool `yaml:"model_invokable,omitempty" json:"modelInvokable,omitempty"`

	// Icon is an optional icon slug (e.g. "rocket") shown in the UI.
	Icon string `yaml:"icon,omitempty" json:"icon,omitempty"`

	// HiddenFromPanel, when true, hides the command from the
	// Settings → Slash Commands list.
	HiddenFromPanel bool `yaml:"hidden_from_panel,omitempty" json:"hiddenFromPanel,omitempty"`

	// Inputs is the ordered list of user-fillable parameters.
	Inputs []UserCommandInput `yaml:"inputs,omitempty" json:"inputs,omitempty"`

	// Body is the human-readable body of the command file.
	//   - kind:text   → the literal text expansion
	//   - kind:prompt → the prompt template (supports {{var}} substitution)
	//   - kind:tool   → optional documentation; the executable spec is
	//                   Tool + ToolArgsTemplate.
	Body string `yaml:"-" json:"body,omitempty"`

	// Tool is the tool name for kind:tool commands (e.g. "bash").
	Tool string `yaml:"tool,omitempty" json:"tool,omitempty"`

	// ToolArgsTemplate is the args template rendered via the substitution
	// grammar before the tool is dispatched. Parsed into a []string;
	// never passed as a raw shell string to prevent injection.
	ToolArgsTemplate string `yaml:"tool_args_template,omitempty" json:"toolArgsTemplate,omitempty"`

	// PayloadPath is the relative path of the markdown body under
	// <DataDir>/slash_commands/ (global) or
	// <DataDir>/projects/<ProjectID>/slash_commands/ (project).
	// Populated by the store; not in the YAML frontmatter.
	PayloadPath string `yaml:"-" json:"payloadPath,omitempty"`

	// CreatedAt and UpdatedAt are Unix timestamps (seconds).
	CreatedAt int64 `yaml:"-" json:"createdAt,omitempty"`
	UpdatedAt int64 `yaml:"-" json:"updatedAt,omitempty"`
}

// UserCommandSummary is the lightweight wire shape returned by List.
// It omits Body + template fields to keep the JSON payload small.
type UserCommandSummary struct {
	Name            string           `json:"name"`
	Scope           UserCommandScope `json:"scope"`
	ProjectID       string           `json:"projectId,omitempty"`
	Kind            UserCommandKind  `json:"kind"`
	Description     string           `json:"description"`
	ModelInvokable  bool             `json:"modelInvokable"`
	Icon            string           `json:"icon,omitempty"`
	HiddenFromPanel bool             `json:"hiddenFromPanel,omitempty"`
	UpdatedAt       int64            `json:"updatedAt,omitempty"`
}

// Summary converts a UserCommand to its lightweight summary form.
func (c UserCommand) Summary() UserCommandSummary {
	return UserCommandSummary{
		Name:            c.Name,
		Scope:           c.Scope,
		ProjectID:       c.ProjectID,
		Kind:            c.Kind,
		Description:     c.Description,
		ModelInvokable:  c.ModelInvokable,
		Icon:            c.Icon,
		HiddenFromPanel: c.HiddenFromPanel,
		UpdatedAt:       c.UpdatedAt,
	}
}
