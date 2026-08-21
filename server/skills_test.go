package server

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Autumn-27/norma/skill"
)

func TestSkillDescriptionsFitListingBudget(t *testing.T) {
	reg, err := skill.LoadDir("../skills")
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	if len(reg.List()) == 0 {
		t.Fatal("no skills loaded")
	}
	for _, sk := range reg.List() {
		if strings.TrimSpace(sk.Description) == "" {
			t.Errorf("skill %q has empty description", sk.Name)
			continue
		}
		if len(sk.Description) > 500 {
			t.Errorf("skill %q description is %d bytes; keep it <= 500 to avoid listing truncation", sk.Name, len(sk.Description))
		}
		if !utf8.ValidString(sk.Description) {
			t.Errorf("skill %q description is not valid UTF-8", sk.Name)
		}
	}
}
