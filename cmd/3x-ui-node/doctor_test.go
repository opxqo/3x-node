package main

import (
	stdbytes "bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorDetectsExposedInternalAPI(t *testing.T) {
	for _, tt := range []struct {
		address string
		exposed bool
	}{
		{"127.0.0.1:62789", false}, {"::1:62789", false}, {"[::1]:62789", false},
		{"0.0.0.0:62789", true}, {":::62789", true}, {"192.168.1.2:62789", true},
	} {
		found, exposed := doctorAPIBindings("tcp 0 0 "+tt.address+" 0.0.0.0:* LISTEN\n", 62789)
		if !found || exposed != tt.exposed {
			t.Fatalf("%s: found=%v exposed=%v", tt.address, found, exposed)
		}
	}
	if found, _ := doctorAPIBindings("tcp 0 0 0.0.0.0:627890 0.0.0.0:* LISTEN", 62789); found {
		t.Fatal("matched another port")
	}
}

func TestDoctorPermissionRepairRecordsModeAndPreservesContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte("private test content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	backup, err := doctorFixPermission(p)
	if err != nil {
		t.Fatal(err)
	}
	i, _ := os.Stat(p)
	if i.Mode().Perm() != 0600 {
		t.Fatal("not restricted")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "private test content" {
		t.Fatal("content changed")
	}
	b, err = os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Path string
		Mode uint32
	}
	if err = json.Unmarshal(b, &record); err != nil || record.Path != p || record.Mode != 0644 {
		t.Fatalf("bad permission backup: %s", b)
	}
	if strings.Contains(string(b), "private test content") {
		t.Fatal("backup leaked content")
	}
	i, _ = os.Stat(backup)
	if i.Mode().Perm() != 0600 {
		t.Fatal("backup permissions")
	}
}

func TestDoctorRejectsLinkedPermissionTargets(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "private")
	link := filepath.Join(d, "link")
	if err := os.WriteFile(p, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err := doctorFixPermission(link); err == nil {
		t.Fatal("accepted symlink")
	}
	if err := os.Link(p, filepath.Join(d, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := doctorFixPermission(p); err == nil {
		t.Fatal("accepted hardlink")
	}
}

func TestDoctorInvalidConfigReportsWithoutLeakingContents(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"token":"never-print-this"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out stdbytes.Buffer
	if err := runDoctor(p, false, strings.NewReader(""), &out); err == nil {
		t.Fatal("invalid config should fail")
	}
	if !strings.Contains(out.String(), "[FAIL] 配置") || strings.Contains(out.String(), "never-print-this") {
		t.Fatalf("bad report: %s", out.String())
	}
}
