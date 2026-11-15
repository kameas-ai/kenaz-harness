//go:build unix

package fsbuiltins

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// swapScenario builds proj/sub/f.txt and state/sub/f.txt, resolves the
// checked path the way the gate does, then swaps proj for a link to state:
// the I/O that follows runs on a path checked before the swap.
func swapScenario(t *testing.T) (checkedFile, state string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proj, state := filepath.Join(base, "proj"), filepath.Join(base, "state")
	for dir, text := range map[string]string{proj: "public", state: "SECRET"} {
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	checkedFile = filepath.Join(proj, "sub", "f.txt")
	if err := os.Rename(proj, proj+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(state, proj); err != nil {
		t.Fatal(err)
	}
	return checkedFile, state
}

func stateEntries(t *testing.T, state string) string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(state, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

// Every primitive refuses a path whose intermediate directory was swapped
// for a link, in every mode the platform has (Linux: openat2 and the
// component walk it falls back to).
func TestNoFollowPrimitives_RefuseIntermediateSwap(t *testing.T) {
	for _, m := range noFollowModes() {
		t.Run(m.name, func(t *testing.T) {
			m.set(t)
			checked, state := swapScenario(t)
			if f, err := openCheckedForRead(checked); !errors.Is(err, ErrPathChanged) {
				if f != nil {
					b, _ := io.ReadAll(f)
					_ = f.Close()
					t.Fatalf("read through the swap returned %q, err %v", b, err)
				}
				t.Fatalf("read err = %v, want ErrPathChanged", err)
			}
			if _, err := openNoFollow(checked); !errors.Is(err, ErrPathChanged) {
				t.Fatalf("openNoFollow err = %v, want ErrPathChanged", err)
			}
			if _, err := openDirNoFollow(filepath.Dir(checked), false); !errors.Is(err, ErrPathChanged) {
				t.Fatalf("openDirNoFollow err = %v, want ErrPathChanged", err)
			}
			if err := atomicWrite(filepath.Join(filepath.Dir(checked), "evil"), []byte("x"), 0o600); !errors.Is(err, ErrPathChanged) {
				t.Fatalf("atomicWrite err = %v, want ErrPathChanged", err)
			}
			if err := atomicWrite(filepath.Join(filepath.Dir(checked), "new", "evil"), []byte("x"), 0o600); !errors.Is(err, ErrPathChanged) {
				t.Fatalf("atomicWrite (mkdir) err = %v, want ErrPathChanged", err)
			}
			if got := stateEntries(t, state); got != "f.txt" {
				t.Fatalf("state/sub = %q after refused writes, want only f.txt", got)
			}
		})
	}
}

// The plain path (no swap) works in every mode, including creating
// missing parent directories.
func TestNoFollowPrimitives_PlainPath(t *testing.T) {
	for _, m := range noFollowModes() {
		t.Run(m.name, func(t *testing.T) {
			m.set(t)
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(base, "a", "b", "c.txt")
			if err := atomicWrite(p, []byte("hello"), 0o640); err != nil {
				t.Fatal(err)
			}
			f, err := openCheckedForRead(p)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(f)
			fi, _ := f.Stat()
			_ = f.Close()
			if string(b) != "hello" || fi.Mode().Perm() != 0o640 {
				t.Fatalf("read back %q mode %v", b, fi.Mode())
			}
			ents, _ := os.ReadDir(filepath.Dir(p))
			if len(ents) != 1 {
				t.Fatalf("temp file left behind: %v", ents)
			}
		})
	}
}

// Mutation proof: with the no-follow on directory components removed, the
// same scenario reads the target. If this ever passes without the leak,
// the refusal tests above have stopped testing anything.
func TestNoFollowPrimitives_MutationIsCaught(t *testing.T) {
	for _, m := range noFollowModes() {
		t.Run(m.name, func(t *testing.T) {
			m.set(t)
			saved := noFollowDir
			noFollowDir &^= unix.O_NOFOLLOW
			restore := mutateFastPath()
			t.Cleanup(func() { noFollowDir = saved; restore() })
			checked, _ := swapScenario(t)
			f, err := openCheckedForRead(checked)
			if err != nil {
				t.Fatalf("mutated read err = %v; the scenario no longer exercises intermediate components", err)
			}
			b, _ := io.ReadAll(f)
			_ = f.Close()
			if string(b) != "SECRET" {
				t.Fatalf("mutated read = %q, want the target's content", b)
			}
		})
	}
}
