package tools

import (
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

var premiseHeadingAliases = map[string]string{
	// Chinese keys
	"题材定位":                    "Genre Positioning",
	"题材和基调":                  "Genre and Tone",
	"核心冲突":                    "Core Conflict",
	"主角目标":                    "Protagonist Goal",
	"结局方向":                    "Ending Direction",
	"终局方向":                    "Ending Direction",
	"写作禁区":                    "Writing Forbidden Zones",
	"差异化卖点":                  "Differentiated Selling Point",
	"差异化钩子":                  "Differentiated Hook",
	"核心兑现承诺":                "Core Promise to Deliver",
	"故事引擎":                    "Story Engine",
	"关系/成长主线":              "Relationship/Growth Mainline",
	"升级路径":                    "Upgrade Path",
	"中段转折":                    "Midpoint Twist",
	"中期转向":                    "Midpoint Twist",
	"终局命题":                    "Ending Theme",
	"短篇适配性":                  "Short Story Suitability",
	"本作为什么适合短篇/单卷收束": "Short Story Suitability",

	// English keys
	"Genre Positioning":           "Genre Positioning",
	"Genre and Tone":              "Genre and Tone",
	"Core Conflict":               "Core Conflict",
	"Protagonist Goal":            "Protagonist Goal",
	"Ending Direction":            "Ending Direction",
	"Writing Forbidden Zones":     "Writing Forbidden Zones",
	"Differentiated Selling Point": "Differentiated Selling Point",
	"Differentiated Hook":         "Differentiated Hook",
	"Core Promise to Deliver":      "Core Promise to Deliver",
	"Story Engine":                "Story Engine",
	"Relationship/Growth Mainline": "Relationship/Growth Mainline",
	"Upgrade Path":                "Upgrade Path",
	"Midpoint Twist":              "Midpoint Twist",
	"Ending Theme":                "Ending Theme",
	"Short Story Suitability":     "Short Story Suitability",
}

func parsePremiseSections(premise string) map[string]string {
	lines := strings.Split(premise, "\n")
	sections := make(map[string]string)
	var current string
	var body []string

	flush := func() {
		if current == "" {
			return
		}
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if text != "" {
			sections[current] = text
		}
		body = body[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if heading, ok := canonicalPremiseHeading(trimmed); ok {
			flush()
			current = heading
			continue
		}
		if current != "" {
			body = append(body, line)
		}
	}
	flush()
	return sections
}

func canonicalPremiseHeading(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimLeft(line, "#"))
	if title == "" {
		return "", false
	}
	canonical, ok := premiseHeadingAliases[title]
	return canonical, ok
}

func premiseStructure(premise string, tier domain.PlanningTier) map[string]any {
	sections := parsePremiseSections(premise)
	required := requiredPremiseHeadings(tier)
	found := make([]string, 0, len(required))
	var missing []string
	for _, heading := range required {
		if _, ok := sections[heading]; ok {
			found = append(found, heading)
			continue
		}
		missing = append(missing, heading)
	}

	structure := map[string]any{
		"template_ready": len(missing) == 0,
		"found":          found,
		"missing":        missing,
	}
	if len(sections) > 0 {
		structure["section_count"] = len(sections)
	}
	return structure
}

func requiredPremiseHeadings(tier domain.PlanningTier) []string {
	common := []string{
		"Genre and Tone",
		"Genre Positioning",
		"Core Conflict",
		"Protagonist Goal",
		"Ending Direction",
		"Writing Forbidden Zones",
		"Differentiated Selling Point",
		"Differentiated Hook",
		"Core Promise to Deliver",
	}

	switch tier {
	case domain.PlanningTierLong:
		return append(common,
			"Story Engine",
			"Relationship/Growth Mainline",
			"Upgrade Path",
			"Midpoint Twist",
			"Ending Theme",
		)
	case domain.PlanningTierMid:
		return append(common,
			"Story Engine",
			"Midpoint Twist",
		)
	case domain.PlanningTierShort:
		return append(common,
			"Short Story Suitability",
		)
	default:
		return common
	}
}
