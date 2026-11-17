package httpapp

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"

	"github.com/NetherQuartz/cu-chi/internal/store"
)

// Android AmneziaWG uses the file stem as a tunnel name, limited to 15 ASCII
// characters. Keep full labels in comments, and a revision-specific suffix so
// devices with similar abbreviated names normally remain distinguishable.
func profileFilename(p store.Profile) string {
	sum := sha256.Sum256([]byte(p.ID))
	suffix := fmt.Sprintf("v%d%x", p.Revision, sum[:2])
	budget := 15 - 3 - len(suffix)
	if budget < 3 {
		return fmt.Sprintf("cc-%x.conf", sum[:6])
	}
	labels := []string{filenameLabel(p.OwnerName, "user"), filenameLabel(p.ServerName, "server"), filenameLabel(p.DeviceName, "device")}
	sizes := []int{1, 1, 1}
	for remaining := budget - 3; remaining > 0; {
		changed := false
		for _, i := range []int{2, 1, 0} {
			if remaining > 0 && sizes[i] < len(labels[i]) {
				sizes[i]++
				remaining--
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return fmt.Sprintf("%s-%s-%s-%s.conf", labels[0][:sizes[0]], labels[1][:sizes[1]], labels[2][:sizes[2]], suffix)
}

func filenameLabel(value, fallback string) string {
	// Russian names are common in this portal. Other unsupported characters are
	// omitted; the field-specific fallback keeps every filename ASCII and safe.
	const cyrillic = "абвгдеёжзийклмнопрстуфхцчшщъыьэюяіїєґ"
	latin := []string{"a", "b", "v", "g", "d", "e", "yo", "zh", "z", "i", "y", "k", "l", "m", "n", "o", "p", "r", "s", "t", "u", "f", "kh", "ts", "ch", "sh", "shch", "", "y", "", "e", "yu", "ya", "i", "yi", "ye", "g"}
	translit := map[rune]string{}
	for i, r := range []rune(cyrillic) {
		translit[r] = latin[i]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if v, ok := translit[r]; ok {
			b.WriteString(v)
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}

func profileMetadata(p store.Profile) string {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
				return ' '
			}
			return r
		}, s)
	}
	return fmt.Sprintf("# Củ Chi\n# User: %s\n# Server: %s\n# Device: %s\n# Revision: %d\n# Issued: %s\n\n", clean(p.OwnerName), clean(p.ServerName), clean(p.DeviceName), p.Revision, p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"))
}
