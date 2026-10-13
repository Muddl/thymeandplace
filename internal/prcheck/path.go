package prcheck

import (
	"path/filepath"
	"regexp"
	"strings"
)

// MatchPath reports whether file matches a git-style glob.
// `**` matches across slashes. `*` matches one path segment.
func MatchPath(pattern, file string) bool {
	pattern = filepath.ToSlash(strings.TrimPrefix(pattern, "/"))
	file = filepath.ToSlash(strings.TrimPrefix(file, "/"))
	re, err := regexp.Compile(globToAnchored(pattern))
	if err != nil {
		return false
	}
	return re.MatchString(file)
}

func globToAnchored(pattern string) string {
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); {
		if strings.HasPrefix(pattern[i:], "**/") {
			b.WriteString("(?:.*/)?")
			i += 3
			continue
		}
		if strings.HasPrefix(pattern[i:], "**") {
			b.WriteString(".*")
			i += 2
			continue
		}
		if pattern[i] == '*' {
			b.WriteString("[^/]*")
			i++
			continue
		}
		b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		i++
	}
	b.WriteByte('$')
	return b.String()
}
