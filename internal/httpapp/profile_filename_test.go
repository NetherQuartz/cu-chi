package httpapp

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/store"
)

func TestProfileDownloadNames(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z0-9-]{1,15}\.conf$`)
	for _, rev := range []int{1, 12, 123, 1234, 12345, 2147483647} {
		p := store.Profile{ID: "00000000-0000-4000-8000-000000000001", OwnerName: "Алиса", ServerName: "Нидерланды", DeviceName: "Телефон / Android", Revision: rev}
		name := profileFilename(p)
		if !valid.MatchString(name) {
			t.Fatalf("incompatible import name %q", name)
		}
		if rev == 1 && !strings.HasPrefix(name, "al-ni-te-v1") {
			t.Fatalf("missing transliterated labels: %s", name)
		}
		if profileFilename(p) != name {
			t.Fatal("unstable download name")
		}
		p.ID = "00000000-0000-4000-8000-000000000002"
		if profileFilename(p) == name {
			t.Fatal("same abbreviated labels lost profile identity")
		}
	}
	for _, name := range []string{"", "☀️手机", "../../CON\r\nInjected: yes", strings.Repeat("Щ", 500)} {
		if got := profileFilename(store.Profile{ID: "fixture", OwnerName: name, ServerName: name, DeviceName: name, Revision: 1}); !valid.MatchString(got) {
			t.Fatalf("unsafe filename %q", got)
		}
	}
}

func TestProfileMetadataCannotInjectConfiguration(t *testing.T) {
	p := store.Profile{OwnerName: "Алиса\r\n[Peer]\n", ServerName: "Test\u2028PrivateKey=bad", DeviceName: "Phone\u2029[Interface]", Revision: 3, CreatedAt: time.Date(2026, 11, 17, 15, 30, 0, 0, time.FixedZone("test", 3*3600))}
	metadata := profileMetadata(p)
	for _, line := range strings.Split(metadata, "\n") {
		if line != "" && !strings.HasPrefix(line, "# ") {
			t.Fatalf("uncommented metadata line: %q", line)
		}
	}
	if !strings.Contains(metadata, "# User: Алиса") || !strings.Contains(metadata, "# Revision: 3") || !strings.Contains(metadata, "2026-11-17T12:30:00Z") {
		t.Fatal("missing labels, revision or UTC issuance time")
	}
}
