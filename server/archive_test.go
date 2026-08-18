package server

import (
	"os"
	"path/filepath"
	"strings"
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

// TestArchiveMessageInvokesSkill covers the happy path against a real DB:
// the skill is on disk and seeded visible to auto, so archiveMessage sends a
// short Skill-invocation trigger. Toggling the visibility off makes it fail
// with an error (no fallback flow).
func TestArchiveMessageInvokesSkill(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	skillDir := t.TempDir()
	writeReportArchiveSkill(t, skillDir)
	s := &Server{m: m, skillDir: skillDir}

	task := &Task{ID: "42", Description: "example.com 渗透测试"}
	msg, err := s.archiveMessage(task)
	if err != nil {
		t.Fatalf("archiveMessage with usable skill: %v", err)
	}
	for _, want := range []string{
		"【报告归档任务】task_id=42",
		`Skill 工具（name="report-archive"）`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("invoke message missing %q:\n%s", want, msg)
		}
	}

	a, err := m.PG().GetAgentByKey("auto")
	if err != nil || a == nil {
		t.Fatalf("auto agent: %v", err)
	}
	if err := m.PG().ToggleSkillVisibility(a.ID, "report-archive", false); err != nil {
		t.Fatal(err)
	}
	msg, err = s.archiveMessage(task)
	if err == nil {
		t.Fatalf("expected error after visibility off, got:\n%s", msg)
	}
}
