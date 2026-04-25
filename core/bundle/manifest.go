package bundle

type Manifest struct {
	Name        string       `yaml:"name" json:"name"`
	Version     string       `yaml:"version" json:"version"`
	Description string       `yaml:"description,omitempty" json:"description,omitempty"`
	DependsOn   []Dependency `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Exports     Exports      `yaml:"exports,omitempty" json:"exports,omitempty"`
	Context     ContextSpec  `yaml:"context,omitempty" json:"context,omitempty"`
	Overrides   []Override   `yaml:"overrides,omitempty" json:"overrides,omitempty"`
}

type Dependency struct {
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
}

type Exports struct {
	Skills []string `yaml:"skills,omitempty" json:"skills,omitempty"`
	Agents []string `yaml:"agents,omitempty" json:"agents,omitempty"`
	MCP    []string `yaml:"mcp,omitempty" json:"mcp,omitempty"`
}

type ContextSpec struct {
	Order []string `yaml:"order,omitempty" json:"order,omitempty"`
}

type Override struct {
	Kind string `yaml:"kind" json:"kind"`
	From string `yaml:"from" json:"from"`
	Use  string `yaml:"use" json:"use"`
}
