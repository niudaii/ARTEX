package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWalkSkillFilesSkipsJunk ensures macOS metadata junk (AppleDouble ._*
// companions, .DS_Store, __MACOSX) never leaks into the skill file tree.
func TestWalkSkillFilesSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"SKILL.md":              "---\nname: demo\n---\n",
		"._SKILL.md":            "appledouble junk",
		".DS_Store":             "finder junk",
		"references/guide.md":   "# guide\n",
		"references/._guide.md": "appledouble junk",
		"__MACOSX/._SKILL.md":   "appledouble junk",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := walkSkillFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[filepath.ToSlash(e)] = true
	}
	want := map[string]bool{"SKILL.md": true, "references/": true, "references/guide.md": true}
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want exactly %v", entries, want)
	}
	for w := range want {
		if !got[w] {
			t.Fatalf("missing %q in %v", w, entries)
		}
	}
}
