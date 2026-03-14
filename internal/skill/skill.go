package skill

import "strings"

// ExtractContent strips YAML frontmatter from a skill file and returns the body.
func ExtractContent(raw string) string {
	lines := strings.Split(raw, "\n")
	inFrontmatter := false
	var result []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			inFrontmatter = !inFrontmatter
			continue
		}
		if !inFrontmatter {
			result = append(result, line)
		}
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}
