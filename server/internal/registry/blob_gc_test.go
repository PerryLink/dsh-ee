package registry

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestCollectBlobsAfterRawOverwrite(t *testing.T) {
	ctx := context.Background()
	st := openRegistryTestStore(t)
	dataDir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	blobs, err := NewBlobs(dataDir)
	must(err)
	put := func(content string) Digest {
		t.Helper()
		d, err := blobs.Put(strings.NewReader(content), 1024)
		must(err)
		return d
	}
	old, current := put("original Raw file"), put("replacement Raw file")
	must(st.CreatePackageSource(ctx, store.PackageSource{Name: "gc-raw", Protocol: "raw", Mode: "hosted"}))
	raw, err := st.PackageSource(ctx, "raw", "gc-raw")
	must(err)
	const file = "releases/artifact.bin"
	for _, d := range []Digest{old, current} {
		f := store.PackageSourceFile{Path: file, BlobKey: d.Key, Size: d.Size, UploaderID: 1}
		must(st.SavePackageSourceFile(ctx, raw.ID, file, "", "", f, true, "", nil, nil))
	}
	npm := put("unpublished npm blob")
	must(st.EnsureUpstreamPackage(ctx, &store.NpmPackage{Name: "gc-npm"}))
	must(st.InsertMirroredVersion(ctx, &store.NpmVersion{
		Name: "gc-npm", Version: "1.0.0", Manifest: []byte("{}"), Integrity: npm.Integrity,
		Shasum: npm.Shasum, BlobKey: npm.Key, Size: npm.Size, Status: "unpublished",
	}))
	kb := put("knowledge base original")
	base := &store.KBase{Slug: "gc-kb", Title: "GC test"}
	must(st.CreateKBase(ctx, base, 1))
	must(st.CreateKDoc(ctx, &store.KDoc{BaseID: base.ID, Title: "Original", BlobKey: kb.Key, Size: kb.Size}, "Text", "", 1))
	staged := put("unpublished Conan blob")
	must(st.CreatePackageSource(ctx, store.PackageSource{Name: "gc-conan", Protocol: "conan", Mode: "hosted"}))
	conan, err := st.PackageSource(ctx, "conan", "gc-conan")
	must(err)
	const parent = "conans/lib/1.0/_/_"
	const revision = parent + "/revisions/abc"
	f := store.PackageSourceFile{Path: revision + "/files/conanfile.py", BlobKey: staged.Key, Size: staged.Size, UploaderID: 1}
	must(st.SavePackageSourceFile(ctx, conan.ID, "lib/_/_", revision, parent, f, false, "", nil, nil))

	temp := filepath.Join(dataDir, "blobs", "tmp", old.Key)
	must(os.WriteFile(temp, []byte("upload in temp"), 0600))
	collect := func(remove bool) map[string]int64 {
		t.Helper()
		found := make(map[string]int64)
		must(CollectBlobs(ctx, dataDir, st, remove, func(key string, size int64) error {
			found[key] = size
			return nil
		}))
		return found
	}
	want := map[string]int64{old.Key: old.Size}
	if found := collect(false); !reflect.DeepEqual(found, want) || !blobs.Has(old.Key) {
		t.Fatalf("dry run: found %v, old blob still present=%v", found, blobs.Has(old.Key))
	}
	if found := collect(true); !reflect.DeepEqual(found, want) || blobs.Has(old.Key) {
		t.Fatalf("delete: found %v, old blob still present=%v", found, blobs.Has(old.Key))
	}
	for _, d := range []Digest{current, npm, kb, staged} {
		if !blobs.Has(d.Key) {
			t.Errorf("referenced blob %s was removed", d.Key)
		}
	}
	_, err = os.Stat(temp)
	must(err)
}
