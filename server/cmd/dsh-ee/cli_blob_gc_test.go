package main

import (
	"strings"
	"testing"
)

func TestCLIBlobGC(t *testing.T) {
	t.Setenv("DSH_EE_DSN", "")
	t.Setenv("DSH_EE_DATA_DIR", "")
	if handled, code, out, stderr := cli("blob-gc", "--help"); !handled || code != 0 || !strings.Contains(out, "blob-gc [--delete]") || stderr != "" {
		t.Fatalf("help: handled=%v, exit=%d, stdout=%q, stderr=%q", handled, code, out, stderr)
	}
	if handled, code, _, stderr := cli("blob-gc", "--delete"); !handled || code != 2 || !strings.Contains(stderr, "DSH_EE_DATA_DIR") {
		t.Fatalf("missing configuration: handled=%v, exit=%d, stderr=%q", handled, code, stderr)
	}
}
