package awg

import (
	_ "embed"
	"regexp"
	"strconv"
	"strings"
)

// The published Amnezia troubleshooting I1 preset, pinned so an application
// upgrade never silently changes the signature previously issued to clients.
//
//go:embed amnezia-i1-v1.txt
var signatureFile string

func DefaultSignature() string { return strings.TrimSpace(signatureFile) }

// Bound both the textual profile (also exported as QR) and the UDP payload.
// Accept only complete CPS expressions supported by the pinned AWG parser.
var signatureTag = regexp.MustCompile(`^<(?:b 0x([0-9A-Fa-f]+)|(?:r|rc|rd) ([0-9]{1,4})|t)>`)

func ValidSignature(value string) bool {
	if len(value) == 0 || len(value) > 1500 {
		return false
	}
	size := 0
	for value != "" {
		tag := signatureTag.FindStringSubmatch(value)
		if tag == nil {
			return false
		}
		switch {
		case tag[1] != "":
			if len(tag[1])%2 != 0 {
				return false
			}
			size += len(tag[1]) / 2
		case tag[2] != "":
			count, _ := strconv.Atoi(tag[2])
			if count == 0 {
				return false
			}
			size += count
		default:
			size += 4
		}
		value = value[len(tag[0]):]
	}
	return size <= 1400
}
