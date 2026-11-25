package store

import (
	"context"
	"errors"
	"testing"
)

func TestPackageSourcePublication(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	source := PackageSource{Name: "cpp", Protocol: "conan", Mode: "hosted"}
	if err := st.CreatePackageSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePackageSource(ctx, source); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate source: %v", err)
	}
	source, err := st.PackageSource(ctx, "conan", "cpp")
	if err != nil {
		t.Fatal(err)
	}
	const parent = "conans/lib/1.0/_/_"
	revision := parent + "/revisions/abc"
	put := func(file, hash string, owner int64, required, archives []string) error {
		f := PackageSourceFile{Path: revision + "/files/" + file, BlobKey: hash, Size: 5, UploaderID: owner}
		return st.SavePackageSourceFile(ctx, source.ID, "lib/_/_", revision, parent, f, false, "", required, archives)
	}
	must := func(err error, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("got %v, want %v", err, want)
		}
	}
	must(put("conanfile.py", "recipe", 1, nil, nil), nil)
	_, err = st.PackageArtifact(ctx, source.ID, revision+"/files/conanfile.py")
	must(err, ErrNotFound)
	required, archives := []string{"conanfile.py"}, []string{"conan_sources"}
	must(put("conanmanifest.txt", "manifest", 1, required, archives), ErrIncompletePackageRevision)
	must(put("conan_sources.tgz", "sources", 2, nil, nil), ErrPackageSourceOwner)
	must(put("conan_sources.tgz", "sources", 1, nil, nil), nil)
	must(put("conanmanifest.txt", "manifest", 1, required, archives), nil)
	must(put("conanmanifest.txt", "manifest", 1, required, archives), nil)
	must(put("conanfile.py", "different", 1, nil, nil), ErrConflict)
	items, err := st.PackageArtifacts(ctx, source.ID, &revision, revision+"/files/conan_sources.tgz", 1)
	if err != nil || len(items) != 1 || items[0].Path != revision+"/files/conanfile.py" {
		t.Fatalf("page = %+v err=%v", items, err)
	}
	revisions, err := st.PackageRevisions(ctx, source.ID, parent, 1)
	if err != nil || len(revisions) != 1 || revisions[0].Path != revision {
		t.Fatalf("revisions = %+v err=%v", revisions, err)
	}
}
