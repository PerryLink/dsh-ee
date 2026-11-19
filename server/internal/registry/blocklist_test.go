package registry

import "testing"

func TestNormalizeBlockEntry(t *testing.T) {
	for in, want := range map[string]string{
		"dsh-foo":       "dsh-foo",
		" @x/foo ":      "@x/foo",
		"@x/*":          "@x/*",
		"author:Alice":  "author:alice",
		"author: bob  ": "author:bob",
	} {
		if got, ok := NormalizeBlockEntry(in); !ok || got != want {
			t.Errorf("%q => %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "author:", "author:a/b", "x/*", "@/*", "Bad Name", "../x"} {
		if got, ok := NormalizeBlockEntry(in); ok {
			t.Errorf("%q should be invalid, got %q", in, got)
		}
	}
}

func TestBlockedBy(t *testing.T) {
	list := []string{"author:alice", "@x/*", "dsh-bad"}
	cases := []struct{ name, author, want string }{
		{"dsh-bad", "", "dsh-bad"},
		{"dsh-bad-2", "", ""},
		{"@x/foo", "", "@x/*"},
		{"@xy/foo", "", ""},
		{"dsh-ok", "Alice", "author:alice"},
		{"dsh-ok", "bob", ""},
	}
	for _, c := range cases {
		if got := blockedBy(list, c.name, c.author); got != c.want {
			t.Errorf("blockedBy(%q, %q) = %q, want %q", c.name, c.author, got, c.want)
		}
	}
}
