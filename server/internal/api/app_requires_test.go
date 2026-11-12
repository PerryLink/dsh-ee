package api

import "testing"

func TestAppRequireFormat(t *testing.T) {
	if got := requireOf("acme/oa", "1.2.3"); got != "acme/oa@^1.2" {
		t.Fatalf("requireOf = %q", got)
	}
	if got := requireOf("acme/oa", "bad"); got != "acme/oa" {
		t.Fatalf("非法版本应只写 id，得到 %q", got)
	}
	id, c := splitRequire(" acme/oa@^1.2 ")
	if id != "acme/oa" || c != "^1.2" {
		t.Fatalf("splitRequire = %q %q", id, c)
	}
	if id, c := splitRequire("acme/oa"); id != "acme/oa" || c != "" {
		t.Fatalf("没写版本：%q %q", id, c)
	}
}
