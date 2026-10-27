package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	in := "key sk-abcdefghijklmnopqrstuvwxyz012345 and Bearer abcdefghijklmnop\n" +
		"-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----\n" +
		"OPENAI_API_KEY=supersecret"
	out := Redact(in)
	for _, bad := range []string{"sk-abcdef", "Bearer abc", "secret", "supersecret"} {
		if strings.Contains(out, bad) {
			t.Fatalf("%q leaked in %q", bad, out)
		}
	}
}

func TestReadPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	body, size, more, err := ReadPage(path, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if body != "234" || size != 10 || !more {
		t.Fatalf("body=%q size=%d more=%v", body, size, more)
	}
	body, size, more, err = ReadPage(path, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	if body != "89" || size != 10 || more {
		t.Fatalf("body=%q size=%d more=%v", body, size, more)
	}
	body, _, more, err = ReadPage(path, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" || more {
		t.Fatalf("body=%q more=%v", body, more)
	}
}

func TestHeadTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 100)+"MIDDLE"+strings.Repeat("z", 100)), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := HeadTail(path, 60, "cx-1:stderr")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "bytes omitted") || !strings.Contains(text, "cx-1:stderr") {
		t.Fatalf("missing elision note: %q", text)
	}
	if !strings.HasPrefix(text, "aaaa") || !strings.HasSuffix(text, "zzzz") {
		t.Fatalf("head/tail not preserved: %q", text)
	}
}
