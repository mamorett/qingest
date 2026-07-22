package normalize

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	controlCharRegex      = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
	collapseNewlinesRegex = regexp.MustCompile(`\n\s*\n\s*\n+`)
	mdPrefixes            = []string{"#", "-", "*", ">", "1.", "2.", "3.", "4.", "5.", "6.", "7.", "8.", "9."}
)

func isMarkdown(s string) bool {
	for _, p := range mdPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func endsWithTerminator(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	return r == '.' || r == '!' || r == '?' || r == ':' || r == ';'
}

func startsWithLowercase(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLower(r)
}

// NormalizeText normalizes text by removing non-printing characters, collapsing newlines, and repairing broken lines.
func NormalizeText(text string) string {
	// Remove BOM if present
	text = strings.TrimPrefix(text, "\ufeff")

	// Standardize newlines
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// Remove control characters
	text = controlCharRegex.ReplaceAllString(text, "")

	// Repair broken paragraph/line spacing
	lines := strings.Split(text, "\n")
	output := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])

		if line == "" {
			if len(output) > 0 && i+1 < len(lines) {
				prevLine := strings.TrimSpace(output[len(output)-1])
				nextLine := strings.TrimSpace(lines[i+1])

				if prevLine != "" && nextLine != "" {
					isPrevMD := isMarkdown(prevLine)
					isNextMD := isMarkdown(nextLine)

					endsTerm := endsWithTerminator(prevLine)
					startsLower := startsWithLowercase(nextLine)

					if !isPrevMD && !isNextMD {
						if !endsTerm || startsLower {
							output[len(output)-1] = prevLine + " " + nextLine
							i += 2
							continue
						}
					}
				}
			}

			output = append(output, lines[i])
			i++
		} else {
			if len(output) > 0 && strings.TrimSpace(output[len(output)-1]) != "" {
				prevLine := strings.TrimSpace(output[len(output)-1])
				isPrevMD := isMarkdown(prevLine)
				isCurrMD := isMarkdown(line)

				endsTerm := endsWithTerminator(prevLine)
				startsLower := startsWithLowercase(line)

				if !isPrevMD && !isCurrMD {
					if !endsTerm || startsLower {
						output[len(output)-1] = prevLine + " " + line
						i++
						continue
					}
				}
			}

			output = append(output, lines[i])
			i++
		}
	}

	text = strings.Join(output, "\n")
	text = collapseNewlinesRegex.ReplaceAllString(text, "\n\n")

	return text
}
