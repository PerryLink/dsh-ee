package extension

import (
	"embed"
	"strings"
)

// 给 Agent 的技能（docs/agent-dev.md §2）。原件在仓库的 extensions/skills/（Claude Code、Codex 直接读那里），
// 这里是编译进二进制的副本，DSH 里作为系统技能下发。改技能时两处一起改，skills_test.go 会核对。
//
//go:embed skills/*/SKILL.md
var skillFiles embed.FS

// Skill 返回一个技能的正文（去掉开头的 frontmatter）和 description。没有这个技能时 ok 为 false。
func Skill(name string) (body, description string, ok bool) {
	raw, err := skillFiles.ReadFile("skills/" + name + "/SKILL.md")
	if err != nil {
		return "", "", false
	}
	text := string(raw)
	if rest, found := strings.CutPrefix(text, "---\n"); found {
		if front, after, found := strings.Cut(rest, "\n---\n"); found {
			for _, line := range strings.Split(front, "\n") {
				if v, found := strings.CutPrefix(line, "description:"); found {
					description = strings.TrimSpace(v)
				}
			}
			text = after
		}
	}
	return strings.TrimLeft(text, "\n"), description, true
}
