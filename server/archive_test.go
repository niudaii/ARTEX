package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArchiveMessage verifies that archiveMessage loads the archive flow from
// skills/report-archive/SKILL.md with {{TASK_ID}} substituted, and falls back
// to the built-in default flow when the skill file is absent or empty.
func TestArchiveMessage(t *testing.T) {
	task := &Task{ID: "42", Description: "example.com 渗透测试", Goal: "拿到 flag"}

	// Skill present: flow body comes from SKILL.md, task id substituted.
	skillDir := t.TempDir()
	md := "---\nname: report-archive\ndescription: test\n---\n\n自定义归档流程 task={{TASK_ID}} end"
	if err := os.MkdirAll(filepath.Join(skillDir, "report-archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "report-archive", "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := (&Server{skillDir: skillDir}).archiveMessage(task)
	for _, want := range []string{
		"【报告归档任务】task_id=42",
		"任务描述：example.com 渗透测试",
		"任务目标：拿到 flag",
		"自定义归档流程 task=42 end",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("archiveMessage missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "{{TASK_ID}}") {
		t.Fatalf("unsubstituted placeholder remains:\n%s", msg)
	}

	// Skill absent: falls back to the built-in default flow.
	msg = (&Server{skillDir: t.TempDir()}).archiveMessage(task)
	for _, want := range []string{
		"按以下阶段执行报告归档",
		`list_task_findings(task_id="42")`,
		`archive_task_report(task_id="42"`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("fallback flow missing %q:\n%s", want, msg)
		}
	}
}
