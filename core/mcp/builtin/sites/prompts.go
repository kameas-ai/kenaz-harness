// Package sites — prompts.go
//
// MCP prompt definitions for the fleet-sites server.
// Prompts are intentionally terse and contain no cloud identifiers,
// endpoint URLs, quota numbers, or runtime enum values (OSS boundary).
// Framework-specific guidance lives in the Fleet mandated_skills channel.
//
// Mission: sites-mcp-server-01NSITE05 WP02.
package sites

import (
	"encoding/json"

	"github.com/kameas-ai/kenaz-harness/core/mcp/builtin/toolserver"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// RegisterPrompts installs the fleet-sites MCP prompts onto srv.
func RegisterPrompts(srv *toolserver.Server) {
	srv.RegisterPrompt(toolserver.PromptSpec{
		Name:        "deploy_a_site",
		Description: "Guide for scaffolding and deploying a site via the fleet-sites tools.",
		Arguments:   []transport.PromptArgument{},
		GetMessages: func(_ map[string]string) ([]json.RawMessage, error) {
			text := `## Deploying a site with fleet-sites

### 1. Scaffold the site
Use sites_init to create a kameas-site.json manifest (and framework stub files for dynamic sites):

  sites_init(path="./my-app", name="my-app", type="dynamic", framework="streamlit")

The name must match the slug grammar: lowercase letters and digits, hyphens as separators.

### 2. Edit the manifest
Open kameas-site.json to review or adjust fields:
- type: "static" (serve pre-built files) or "dynamic" (run a server process)
- static.dir: directory containing built assets (default: "dist")
- dynamic.runtime: language runtime (e.g. "python3.12")
- dynamic.framework: preset entrypoint wiring ("streamlit", "dash", "custom")
- dynamic.entrypoint: command to start the server (required for "custom")
- dynamic.build.command: build step (e.g. "pip install -r requirements.txt")
- ignore: additional glob patterns to exclude from the bundle

### 3. Deploy
  sites_deploy(path="./my-app")

sites_deploy validates the manifest, packs the directory into a bundle,
uploads it to Fleet, and polls for completion. The response includes
the site URL from the server.

### 4. Check status and logs
  sites_status(site="my-app")
  sites_logs(site="my-app", tail_lines=200)

### 5. List and delete
  sites_list()
  sites_delete(site="my-app", confirm=true)

### Notes
- sites_init works offline; all other tools require Fleet sign-in.
- Secrets must be injected via the Fleet console or API, never in the manifest.
- The "ignore" field and the default ignore set exclude .env*, *.pem, *.key, .git, node_modules.
- sites_delete requires confirm:true and cannot be undone.`

			msg, _ := json.Marshal(map[string]any{
				"role": "user",
				"content": map[string]any{
					"type": "text",
					"text": text,
				},
			})
			return []json.RawMessage{msg}, nil
		},
	})
}
