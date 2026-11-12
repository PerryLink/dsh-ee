package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 编译进二进制的技能要和 extensions/skills/ 下的原件一致。
func TestSkillsMatchRepository(t *testing.T) {
	for _, name := range []string{"dsh-ee-extension-dev", "dsh-ee-access-config"} {
		want, err := os.ReadFile(filepath.Join("..", "..", "..", "extensions", "skills", name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := skillFiles.ReadFile("skills/" + name + "/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("server/internal/extension/skills/%s/SKILL.md 和 extensions/skills/%s/SKILL.md 不一样：改技能时两处一起改", name, name)
		}
		body, desc, ok := Skill(name)
		if !ok || desc == "" || strings.HasPrefix(body, "---") || !strings.HasPrefix(body, "# ") {
			t.Errorf("%s：frontmatter 没去掉或没有 description", name)
		}
	}
}
