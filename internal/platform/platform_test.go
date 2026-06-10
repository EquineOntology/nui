package platform

import "testing"

// TestOpenCopyEmptyURL verifies the empty-URL guard so the TUI can call these
// uniformly and get an error (not a silent no-op or a spawned process) when an
// item has no URL.
func TestOpenCopyEmptyURL(t *testing.T) {
	if err := OpenURL(""); err == nil {
		t.Fatalf("OpenURL(\"\") should error")
	}
	if err := OpenURL("   "); err == nil {
		t.Fatalf("OpenURL(whitespace) should error")
	}
	if err := CopyURL(""); err == nil {
		t.Fatalf("CopyURL(\"\") should error")
	}
}

// TestOpenCommandPerOS sanity-checks the per-OS command selection on the host
// this test runs on (we cannot vary runtime.GOOS), confirming a non-empty
// command name and that the URL is passed through.
func TestOpenCommandPerOS(t *testing.T) {
	name, args := openCommand("https://example.com")
	if name == "" {
		t.Skipf("unsupported test platform; open command is a no-op")
	}
	found := false
	for _, a := range args {
		if a == "https://example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("open command %q args %v should carry the URL", name, args)
	}
}
