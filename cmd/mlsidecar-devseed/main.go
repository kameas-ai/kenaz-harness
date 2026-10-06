// Command mlsidecar-devseed installs a locally built or locally fetched
// kenaz-ml engine onedir into the DEV engine root (~/.kenaz/ml/dev) so the
// harness's own sidecar lifecycle launches it on the dev lane (base :7785,
// falling back to :7795, :7805, … past a foreign listener; the chosen port
// is recorded in <root>/engine.port), exactly as it would a released
// engine. scripts/dev-ml.sh drives it before `wails dev`.
//
// It writes a ProvenanceDeveloperBuild install record, which only the dev
// root accepts (mlsidecar.Layout.DeveloperBuilds); a prod or test root
// refuses the same record, so this can never make a release build adopt an
// unsigned engine. See core/mlsidecar/devseed.go.
//
//	go run ./cmd/mlsidecar-devseed --onedir ../kenaz-ml/dist/kameas-ml
//	go run ./cmd/mlsidecar-devseed --onedir /path/to/kameas-ml --source gh-artifact:123
//	go run ./cmd/mlsidecar-devseed --status
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mlsidecar-devseed:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("mlsidecar-devseed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	onedir := fs.String("onedir", "", "kameas-ml onedir to install (launcher + _internal/ + VERSION)")
	version := fs.String("version", "", "override the onedir's VERSION file")
	source := fs.String("source", "", "where the bytes came from, recorded in install.json (default local:<path>)")
	note := fs.String("note", "", "free-text note recorded in install.json")
	root := fs.String("root", "", "engine install root (default ~/.kenaz/ml/dev)")
	env := fs.String("env", mlsidecar.EngineEnvDev, "engine env the root belongs to: dev or test (never prod)")
	status := fs.Bool("status", false, "print the root's current install record as JSON and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *env == mlsidecar.EngineEnvProd {
		return errors.New("refusing to seed the prod engine root: developer builds are dev/test only")
	}
	if *root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		r, err := mlsidecar.DefaultRootFor(home, *env)
		if err != nil {
			return err
		}
		*root = r
	}
	layout := mlsidecar.NewLayout(*root)
	layout.DeveloperBuilds = true

	if *status {
		rec, ok, err := mlsidecar.ReadInstallJSON(layout)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(stdout, "{\"installed\":false,\"root\":%q}\n", layout.Root)
			return nil
		}
		verr := error(nil)
		if _, verr = mlsidecar.VerifyInstalled(layout, rec.Version, nil); verr != nil {
			rec.Verification = rec.Verification + " [VERIFY FAILED: " + verr.Error() + "]"
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"installed": true, "verifies": verr == nil, "root": layout.Root, "record": rec})
	}

	if *onedir == "" {
		return errors.New("--onedir is required (or --status)")
	}
	res, err := mlsidecar.SeedDeveloperBuild(layout, mlsidecar.SeedRequest{
		Onedir: *onedir, Version: *version, Source: *source, Note: *note,
	})
	if err != nil {
		return err
	}
	if res.AlreadyCurrent {
		fmt.Fprintf(stdout, "engine %s already seeded in %s (tree %s); nothing to do\n", res.Record.Version, layout.Root, res.Record.TreeSHA256)
		return nil
	}
	fmt.Fprintf(stdout, "seeded engine %s into %s (tree %s, base port %d)\n", res.Record.Version, layout.Root, res.Record.TreeSHA256, mlsidecar.EnginePort(*env))
	return nil
}
