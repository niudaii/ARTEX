package server

import (
	"os"
	"path/filepath"
	"testing"
)

// writeReportArchiveSkill drops a minimal report-archive SKILL.md into dir.
func writeReportArchiveSkill(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "report-archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: report-archive\ndescription: test\n---\n\n归档流程正文"
	if err := os.WriteFile(filepath.Join(dir, "report-archive", "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestArchiveSkillOnDisk checks the filesystem half of archiveSkillUsable.
func TestArchiveSkillOnDisk(t *testing.T) {
	dir := t.TempDir()
	if archiveSkillOnDisk(dir) {
		t.Fatal("empty dir should not report the skill")
	}
	writeReportArchiveSkill(t, dir)
	if !archiveSkillOnDisk(dir) {
		t.Fatal("skill with instructions should be reported")
	}
	// frontmatter only → no instructions → not usable
	if err := os.WriteFile(filepath.Join(dir, "report-archive", "SKILL.md"), []byte("---\nname: report-archive\ndescription: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if archiveSkillOnDisk(dir) {
		t.Fatal("skill without instructions should not be reported")
	}
}

// TestArchiveMessageNoSkillErrors covers the no-skill path: without the
// report-archive skill on disk, archiveMessage returns an error instead of
// silently degrading to a built-in flow (fallback removed).
func TestArchiveMessageNoSkillErrors(t *testing.T) {
	task := &Task{ID: "42", Description: "example.com 渗透测试", Goal: "拿到 flag"}
	msg, err := (&Server{skillDir: t.TempDir()}).archiveMessage(task)
	if err == nil {
		t.Fatalf("expected error without skill, got message:\n%s", msg)
	}
	if msg != "" {
		t.Fatalf("expected empty message on error, got:\n%s", msg)
	}
}
