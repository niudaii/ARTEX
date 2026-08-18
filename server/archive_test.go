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

// TestArchiveMessageFallback covers the degraded path: without a DB or skill
// file the built-in flow is injected with the task id substituted.
func TestArchiveMessageFallback(t *testing.T) {
	task := &Task{ID: "42", Description: "example.com 渗透测试", Goal: "拿到 flag"}
	msg := (&Server{skillDir: t.TempDir()}).archiveMessage(task)
	for _, want := range []string{
		"【报告归档任务】task_id=42",
		"任务描述：example.com 渗透测试",
		"任务目标：拿到 flag",
		"按以下阶段执行报告归档",
		`list_task_findings(task_id="42")`,
		`archive_task_report(task_id="42"`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("fallback message missing %q:\n%s", want, msg)
		}
	}
}

// TestArchiveMessageInvokesSkill covers the happy path against a real DB:
// the skill is on disk and seeded visible to auto, so archiveMessage sends a
// short Skill-invocation trigger instead of injecting the flow. Toggling the
// visibility off drops back to the injected flow.
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
	msg := s.archiveMessage(task)
	for _, want := range []string{
		"【报告归档任务】task_id=42",
		`Skill 工具（name="report-archive"）`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("invoke message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "按以下阶段执行报告归档") {
		t.Fatalf("full flow injected although the skill is usable:\n%s", msg)
	}

	a, err := m.PG().GetAgentByKey("auto")
	if err != nil || a == nil {
		t.Fatalf("auto agent: %v", err)
	}
	if err := m.PG().ToggleSkillVisibility(a.ID, "report-archive", false); err != nil {
		t.Fatal(err)
	}
	msg = s.archiveMessage(task)
	if !strings.Contains(msg, "按以下阶段执行报告归档") || !strings.Contains(msg, `list_task_findings(task_id="42")`) {
		t.Fatalf("expected injected fallback flow after visibility off:\n%s", msg)
	}
}
