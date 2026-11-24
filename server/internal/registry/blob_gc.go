package registry

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// CollectBlobs reports unreferenced blobs. All writers must be stopped until it returns.
func CollectBlobs(ctx context.Context, dataDir string, st *store.Store, remove bool, report func(key string, size int64) error) error {
	root, err := os.OpenRoot(filepath.Join(dataDir, "blobs"))
	if err != nil {
		return err
	}
	defer root.Close()
	keys, err := st.BlobReferences(ctx)
	if err != nil {
		return fmt.Errorf("读取 blob 引用: %w", err)
	}
	return fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path == "tmp" || strings.Count(path, "/") > 1 {
				return fs.SkipDir
			}
			return nil
		}
		key := entry.Name()
		if !entry.Type().IsRegular() || !blobKey.MatchString(key) || path != key[:2]+"/"+key[2:4]+"/"+key {
			return nil
		}
		if _, referenced := keys[key]; referenced {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := report(key, info.Size()); err != nil {
			return err
		}
		if remove {
			return root.Remove(filepath.FromSlash(path))
		}
		return nil
	})
}
