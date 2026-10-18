package cursor

import (
	"regexp"
	"strings"
)

var labelAnnotation = regexp.MustCompile(`\s*(\([^()]*\)|\[[^\[\]]*\])$`)
var labelControl = regexp.MustCompile(`(?i)^(extra[ -]+high|xhigh|minimal|medium|thinking|high|low|max|none|fast)$`)
var labelControlSuffix = regexp.MustCompile(`(?i)[\s_-]+(extra[\s_-]+high|xhigh|minimal|medium|thinking|high|low|max|none|fast)$`)
var labelContext = regexp.MustCompile(`(?i)^(?:(?:context(?: window)?|window)\s*[:=]?\s*)?(?:[0-9][0-9,.]*\s*[km](?:\s*(?:tokens?|context(?: window)?))?|[0-9][0-9,]*\s+tokens?)$`)
var labelContextSuffix = regexp.MustCompile(`(?i)\s+(?:[0-9][0-9,.]*\s*[km](?:\s*(?:tokens?|context(?: window)?))?|[0-9][0-9,]*\s+tokens?)$`)

func familyDisplayName(label string, hasControls bool) string {
	label = strings.Join(strings.Fields(label), " ")
	var annotations []string
	for {
		match := labelAnnotation.FindStringSubmatchIndex(label)
		if match != nil {
			note := label[match[2]:match[3]]
			value := note[1 : len(note)-1]
			label = strings.TrimSpace(label[:match[0]])
			if !strings.EqualFold(value, "cursor") && !strings.EqualFold(value, "no zdr") && !labelContext.MatchString(value) && !(hasControls && labelControl.MatchString(value)) {
				annotations = append([]string{note}, annotations...)
			}
			continue
		}
		if hasControls {
			trimmed := strings.TrimSpace(labelControlSuffix.ReplaceAllString(label, ""))
			if trimmed != label && trimmed != "" {
				label = trimmed
				continue
			}
		}
		trimmed := strings.TrimSpace(labelContextSuffix.ReplaceAllString(label, ""))
		if trimmed != label && trimmed != "" {
			label = trimmed
			continue
		}
		break
	}
	for _, note := range annotations {
		label += " " + note
	}
	return label
}
