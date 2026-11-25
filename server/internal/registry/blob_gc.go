package registry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 配对闸门（审计 C1）：blob-gc 只有在「数据库与数据目录是同一套实例」时才能删除。
// 两种常见错配都会让所有 tarball 被判成未引用：
//   - 空库/新库 + 正确的数据目录：数据库里没有任何引用；
//   - 别的库 + 正确的数据目录：库里的引用 key 在本目录里基本不存在。
//
// 闸门只拦删除；预览（Remove=false）只在 Gate 里提示，方便先核对目标再决定。
var ErrUnsafeDeletion = errors.New("blob-gc：数据目录与数据库疑似不配对，拒绝删除")

// GateKind 是配对闸门的种类。
type GateKind string

const (
	// GateNoReferences 是闸门①：数据库里一条 blob 引用都没有，目录里却有候选。
	GateNoReferences GateKind = "no-references"
	// GateUnpaired 是闸门②：抽样的引用 blob 大多不在数据目录里。
	GateUnpaired GateKind = "unpaired"
)

// maxReferencedSample 是闸门②最多检查多少个引用 key。
const maxReferencedSample = 20

// unpairedMissingPercent 是闸门②的判定阈值：抽样里缺失比例达到 90% 即认为库与目录不配对。
const unpairedMissingPercent = 90

// GateResult 描述一次没过的配对闸门。
type GateResult struct {
	Kind          GateKind
	Referenced    int // 数据库里的引用总数
	Sampled       int // 闸门②实际抽样检查的引用数
	SampleMissing int // 抽样里不在数据目录的个数
}

// BlobRefSource 是 blob-gc 需要的数据库能力（*store.Store 实现了它）。
type BlobRefSource interface {
	BlobReferences(ctx context.Context) (map[string]struct{}, error)
}

// GCOptions 控制一次 blob 扫描/清理。
type GCOptions struct {
	// MinAge 跳过修改时间在 Now()-MinAge 之后的文件，用来缓解「blob 先落盘、
	// 数据库行后提交」期间新文件被误删（审计 C2）；0 表示不按时间跳过。
	MinAge time.Duration
	// Remove 为 true 时允许删除候选；false 只收集候选（预览）。
	Remove bool
	// Force 越过配对闸门；只在人工确认数据库与数据目录配对后使用。
	Force bool
	// Now 返回当前时间；测试可注入，nil 表示 time.Now。
	Now func() time.Time
}

func (o GCOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Candidate 是一个未引用、可删除的 blob 文件。
type Candidate struct {
	Key  string
	Size int64
}

// BlobScan 是一次扫描的结果：候选、计数与闸门结论都在删除之前算好。
// 删除前所有检查都已完成，调用方可以先打印目标与计数，再决定是否删除。
type BlobScan struct {
	BlobsDir   string      // dataDir/blobs 的绝对路径
	Referenced int         // 数据库里的 blob 引用数
	Scanned    int         // blobs/ 里符合布局的 blob 文件数
	Candidates []Candidate // 未引用且过了 MinAge 的文件
	TooNew     int         // 未引用但因 MinAge 跳过的文件数
	Gate       *GateResult // nil 表示配对检查通过；非 nil 表示删除不安全

	root *os.Root
}

// ScanBlobs 扫描 dataDir/blobs 与数据库引用，收集候选并跑配对闸门，不删除任何文件。
//
// 未通过闸门、opts.Remove 为 true 且 opts.Force 为 false 时，返回值同时包含非 nil 的
// *BlobScan（供打印目标与计数）和包装了 ErrUnsafeDeletion 的错误；预览模式只把结论放进
// Gate，不返回错误。调用方必须用 (*BlobScan).Close 释放目录句柄。
func ScanBlobs(ctx context.Context, dataDir string, refs BlobRefSource, opts GCOptions) (*BlobScan, error) {
	blobsDir, err := filepath.Abs(filepath.Join(dataDir, "blobs"))
	if err != nil {
		return nil, fmt.Errorf("解析数据目录: %w", err)
	}
	root, err := os.OpenRoot(blobsDir)
	if err != nil {
		return nil, fmt.Errorf("打开数据目录 %s: %w", blobsDir, err)
	}
	scan := &BlobScan{BlobsDir: blobsDir, root: root}

	keys, err := refs.BlobReferences(ctx)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("读取 blob 引用: %w", err)
	}
	scan.Referenced = len(keys)
	now := opts.now()
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
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
		scan.Scanned++
		if _, referenced := keys[key]; referenced {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// 宽限期内的文件先不动：写入是「blob 先落盘、数据库行后提交」，扫描之后、
		// 删除之前新落的 blob 可能还没有任何行引用。
		if opts.MinAge > 0 && info.ModTime().After(now.Add(-opts.MinAge)) {
			scan.TooNew++
			return nil
		}
		scan.Candidates = append(scan.Candidates, Candidate{Key: key, Size: info.Size()})
		return nil
	})
	if err != nil {
		_ = root.Close()
		return nil, err
	}

	scan.Gate = pairingGate(scan, keys, root)
	if scan.Gate != nil && opts.Remove && !opts.Force {
		return scan, fmt.Errorf("%w（%s）", ErrUnsafeDeletion, scan.Gate.Kind)
	}
	return scan, nil
}

// pairingGate 跑两条配对闸门；只有存在候选（确实要删东西）时才可能返回非 nil。
func pairingGate(scan *BlobScan, keys map[string]struct{}, root *os.Root) *GateResult {
	if len(scan.Candidates) == 0 {
		return nil
	}
	if len(keys) == 0 {
		return &GateResult{Kind: GateNoReferences}
	}
	sample := sampleBlobKeys(keys, maxReferencedSample)
	missing := 0
	for _, key := range sample {
		// 引用 key 不合法时按缺失算：库里的引用一个都用不上，同样说明不配对。
		if !blobKey.MatchString(key) {
			missing++
			continue
		}
		if _, err := root.Stat(key[:2] + "/" + key[2:4] + "/" + key); errors.Is(err, fs.ErrNotExist) {
			missing++
		}
	}
	if missing*100 >= len(sample)*unpairedMissingPercent {
		return &GateResult{Kind: GateUnpaired, Referenced: len(keys), Sampled: len(sample), SampleMissing: missing}
	}
	return nil
}

// sampleBlobKeys 从引用里均匀抽样，最多 limit 个；先排序保证结果可复现。
func sampleBlobKeys(keys map[string]struct{}, limit int) []string {
	all := make([]string, 0, len(keys))
	for key := range keys {
		all = append(all, key)
	}
	sort.Strings(all)
	if len(all) <= limit {
		return all
	}
	sample := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		sample = append(sample, all[i*len(all)/limit])
	}
	return sample
}

// DeleteError 是一个删除失败的 blob。
type DeleteError struct {
	Key string
	Err error
}

// DeleteResult 汇总一次逐项删除。
type DeleteResult struct {
	Deleted int
	Failed  int
	Errors  []DeleteError
}

// Delete 逐个删除候选。单个文件失败（权限、非空目录等）不会中止整趟；文件已经不在了
// （ENOENT）视为删除成功。ctx 取消时停止，不再尝试剩余候选。
func (s *BlobScan) Delete(ctx context.Context) DeleteResult {
	var res DeleteResult
	for _, c := range s.Candidates {
		if err := ctx.Err(); err != nil {
			res.Failed++
			res.Errors = append(res.Errors, DeleteError{Key: c.Key, Err: err})
			break
		}
		err := s.root.Remove(c.Key[:2] + "/" + c.Key[2:4] + "/" + c.Key)
		switch {
		case err == nil, errors.Is(err, fs.ErrNotExist):
			res.Deleted++
		default:
			res.Failed++
			res.Errors = append(res.Errors, DeleteError{Key: c.Key, Err: err})
		}
	}
	return res
}

// Close 释放扫描期间打开的数据目录句柄；可重复调用。
func (s *BlobScan) Close() error {
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	return err
}
