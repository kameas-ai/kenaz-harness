// Package sites implements the fleet-sites MCP server — a thin stdio
// JSON-RPC server that exposes 6 site-management tools over the
// sites-foundation-01NSITE04 core/sites + core/fleet/sites.go layer.
//
// The server is spawned as `harness mcp sites` (no Wails, no SQLite,
// no core.New). It loads tokens from the OS keychain and capability
// state from the disk cache at startup.
//
// OSS boundary: no cloud identifiers, endpoint URLs, quota constants,
// or runtime enum values in this package. Wire errors from the Fleet API
// are surfaced verbatim (per contract §5).
//
// Mission: sites-mcp-server-01NSITE05 WP02.
package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mcp/builtin/toolserver"
	coresites "github.com/kameas-ai/kenaz-harness/core/sites"
)

// ServerName is the MCP server name used in the initialize response.
const ServerName = "fleet-sites"

// ServerVersion is the sites server version.
const ServerVersion = "0.1.0"

// RegisterAll wires 6 tool handlers onto srv. The fleet client and dataDir
// are captured by each handler; handlers that need fleet calls check
// auth/capability state at call time.
//
// Tools registered:
//   - sites_init        (fully offline; no fleet calls)
//   - sites_deploy      (validate manifest → pack → upload → optional poll)
//   - sites_list        (list org sites)
//   - sites_status      (site record + recent deployments)
//   - sites_logs        (build + runtime log tail)
//   - sites_delete      (requires confirm:true)
func RegisterAll(srv *toolserver.Server, fc *fleet.Client, dataDir string) {
	srv.Register(toolserver.ToolSpec{
		Name:        "sites_init",
		Description: "Scaffold a new site directory with kameas-site.json (and framework stub files for dynamic sites). Works fully offline — no Fleet connection needed.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Directory path to create or use as the site root. Created if it does not exist."
				},
				"name": {
					"type": "string",
					"description": "Site slug: lowercase letters and digits, hyphens as internal separators (pattern ^[a-z0-9]+(-[a-z0-9]+)*$, max 40 chars)."
				},
				"type": {
					"type": "string",
					"enum": ["static", "dynamic"],
					"description": "Site kind: 'static' (pre-built files) or 'dynamic' (running server process)."
				},
				"framework": {
					"type": "string",
					"enum": ["streamlit", "dash", "custom"],
					"description": "Framework preset for dynamic sites. Fills in entrypoint, health path, and stub files. Required when type is dynamic."
				}
			},
			"required": ["path", "name", "type"]
		}`),
		Handler: handleSitesInit,
	})

	srv.Register(toolserver.ToolSpec{
		Name:        "sites_deploy",
		Description: "Validate the site manifest, pack the directory into a bundle, and upload it to Fleet for deployment. Polls for completion by default.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Path to the site root directory (must contain kameas-site.json)."
				},
				"wait": {
					"type": "boolean",
					"description": "Wait for the deployment to reach 'live' or 'failed' status before returning. Default: true.",
					"default": true
				},
				"timeout_seconds": {
					"type": "integer",
					"description": "Maximum seconds to wait when wait=true. Default: 300.",
					"default": 300
				}
			},
			"required": ["path"]
		}`),
		Handler: makeHandleSitesDeploy(fc, dataDir),
	})

	srv.Register(toolserver.ToolSpec{
		Name:        "sites_list",
		Description: "List all sites in the signed-in org.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Handler:     makeHandleSitesList(fc),
	})

	srv.Register(toolserver.ToolSpec{
		Name:        "sites_status",
		Description: "Get the current status of a site including active deployment details.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"site": {
					"type": "string",
					"description": "Site ID or slug."
				},
				"deployment_id": {
					"type": "string",
					"description": "Optional deployment ID. When provided, returns status of that specific deployment."
				}
			},
			"required": ["site"]
		}`),
		Handler: makeHandleSitesStatus(fc),
	})

	srv.Register(toolserver.ToolSpec{
		Name:        "sites_logs",
		Description: "Fetch build and runtime logs for a dynamic site deployment. Returns 404 for static sites.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"site": {
					"type": "string",
					"description": "Site ID or slug."
				},
				"deployment_id": {
					"type": "string",
					"description": "Optional deployment ID to fetch logs for a specific deployment."
				},
				"tail_lines": {
					"type": "integer",
					"description": "Number of log lines to tail (maximum 2000). Default: 100.",
					"default": 100,
					"maximum": 2000
				}
			},
			"required": ["site"]
		}`),
		Handler: makeHandleSitesLogs(fc),
	})

	srv.Register(toolserver.ToolSpec{
		Name:        "sites_delete",
		Description: "Delete a site. The site is tombstoned and artifacts are garbage-collected after 7 days. Requires confirm:true.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"site": {
					"type": "string",
					"description": "Site ID or slug."
				},
				"confirm": {
					"type": "boolean",
					"description": "Must be exactly true to confirm deletion. Any other value returns an error without calling the API."
				}
			},
			"required": ["site", "confirm"]
		}`),
		Handler: makeHandleSitesDelete(fc),
	})
}

// ----- sites_init handler (fully offline) -----

type sitesInitArgs struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Framework string `json:"framework"`
}

func handleSitesInit(ctx context.Context, raw json.RawMessage) (any, error) {
	var args sitesInitArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("sites_init: invalid arguments: %w", err)
	}
	if args.Path == "" {
		return nil, fmt.Errorf("sites_init: path is required")
	}
	if args.Name == "" {
		return nil, fmt.Errorf("sites_init: name is required")
	}
	if args.Type == "" {
		return nil, fmt.Errorf("sites_init: type is required (\"static\" or \"dynamic\")")
	}

	// Create directory if it doesn't exist.
	absPath, err := filepath.Abs(args.Path)
	if err != nil {
		return nil, fmt.Errorf("sites_init: resolve path %q: %w", args.Path, err)
	}
	if err := os.MkdirAll(absPath, 0o755); err != nil {
		return nil, fmt.Errorf("sites_init: create directory %q: %w", absPath, err)
	}

	kind := coresites.SiteKind(args.Type)
	framework := coresites.Framework(args.Framework)

	// Build manifest.
	manifest := buildInitManifest(args.Name, kind, framework)

	// Validate the manifest before writing.
	if err := coresites.Validate(manifest); err != nil {
		return nil, fmt.Errorf("sites_init: %w", err)
	}

	manifestPath := filepath.Join(absPath, "kameas-site.json")
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sites_init: marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil { //nolint:gosec
		return nil, fmt.Errorf("sites_init: write kameas-site.json: %w", err)
	}

	created := []string{"kameas-site.json"}

	// Write framework stub files for dynamic sites.
	if kind == coresites.KindDynamic && framework != "" && framework != coresites.FrameworkCustom {
		stubs, err := writeFrameworkStubs(absPath, framework)
		if err != nil {
			return nil, fmt.Errorf("sites_init: write framework stubs: %w", err)
		}
		created = append(created, stubs...)
	}

	return map[string]any{
		"path":    absPath,
		"created": created,
		"message": fmt.Sprintf("Site %q initialized at %s. Edit kameas-site.json and run sites_deploy when ready.", args.Name, absPath),
	}, nil
}

// buildInitManifest constructs a Manifest suitable for scaffolding.
func buildInitManifest(name string, kind coresites.SiteKind, framework coresites.Framework) coresites.Manifest {
	m := coresites.Manifest{
		Version: 1,
		Name:    name,
		Type:    kind,
	}
	switch kind {
	case coresites.KindStatic:
		m.Static = &coresites.StaticSection{Dir: "dist"}
	case coresites.KindDynamic:
		d := coresites.DynamicSection{
			Runtime:   "python3.12",
			Framework: framework,
		}
		d = coresites.ApplyFrameworkPresets(d)
		m.Dynamic = &d
	}
	return m
}

// writeFrameworkStubs creates framework-specific stub files.
// Returns the list of relative file names created.
func writeFrameworkStubs(dir string, framework coresites.Framework) ([]string, error) {
	var created []string
	switch framework {
	case coresites.FrameworkStreamlit:
		files := map[string]string{
			"requirements.txt": "streamlit\n",
			"app.py": strings.Join([]string{
				"import streamlit as st",
				"",
				"st.title(\"My App\")",
				"st.write(\"Hello from Fleet Sites!\")",
				"",
			}, "\n"),
		}
		for name, content := range files {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); os.IsNotExist(err) {
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec
					return nil, fmt.Errorf("write %s: %w", name, err)
				}
				created = append(created, name)
			}
		}
	case coresites.FrameworkDash:
		files := map[string]string{
			"requirements.txt": "dash\ngunicorn\n",
			"app.py": strings.Join([]string{
				"from dash import Dash, html",
				"",
				"app = Dash(__name__)",
				"server = app.server",
				"",
				"app.layout = html.Div([",
				"    html.H1(\"My Dash App\"),",
				"    html.P(\"Hello from Fleet Sites!\"),",
				"])",
				"",
				"if __name__ == \"__main__\":",
				"    app.run(debug=True)",
				"",
			}, "\n"),
		}
		for name, content := range files {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); os.IsNotExist(err) {
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec
					return nil, fmt.Errorf("write %s: %w", name, err)
				}
				created = append(created, name)
			}
		}
	}
	return created, nil
}

// ----- gated tool helpers -----

// authCheck returns a clean error message when the fleet client is disabled,
// not signed in, or lacks the required capability. Returns nil when the call
// should proceed.
//
// Per FR-101 these are returned as handler errors (isError:true), not
// JSON-RPC errors, so the caller's tool loop sees them as normal tool results.
func authCheck(fc *fleet.Client, caps fleet.Capabilities) error {
	if fc == nil || fleet.Disabled() {
		return fmt.Errorf("Fleet integration is disabled (HARNESS_FLEET_DISABLED=1). sites_init still works offline.")
	}
	_, err := fleet.LoadTokens()
	if err != nil {
		return fmt.Errorf("Not signed in to Fleet. Sign in from the harness (Settings → Fleet) and retry.")
	}
	if !caps.Has(fleet.CapSitesHosting) {
		return fmt.Errorf("Fleet Sites requires an Enterprise plan (capability sites_hosting). Contact your org admin.")
	}
	return nil
}

// loadCaps loads capabilities from the disk cache. Does not block on network.
func loadCaps(dataDir string) fleet.Capabilities {
	if dataDir == "" {
		return fleet.DefaultDenyCapabilities()
	}
	caps, err := fleet.LoadCapabilities(dataDir)
	if err != nil {
		return fleet.DefaultDenyCapabilities()
	}
	return caps
}
