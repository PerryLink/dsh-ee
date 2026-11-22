package registry

import (
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 缓存用量从磁盘重扫的最小间隔。多实例共用同一块盘时，别的实例写入的 tarball
// 不属于本实例的累加计数；每 5 分钟（或下一次写盘前）重扫一次，把长期低估压缩到
// 一个扫描周期。只在有写盘请求时才扫，没有写入就不耗 IO。
const defaultBlobRescanInterval = 5 * time.Minute

// Blobs 是按内容寻址的 tarball 存储：文件名就是 sha512，同一个包被多个名字引用也只存一份。
// 写入先落临时文件再改名，进程中途退出不会留下半个文件。
//
// used 记录已落盘 tarball 的累计字节数，用来实现缓存总量上限（见 Service.MaxCacheBytes）。
// 启动时扫一遍目录对齐磁盘，之后每次新增 blob 时累加；同一个内容重复写入只算一次；
// 距上次全量扫描超过 rescanEvery 时，写盘前的用量检查会先从磁盘重扫一次（见 RefreshIfStale）。
type Blobs struct {
	dir string
	// putMu 串行化「目标文件是否已存在 → 改名落盘 → 累加用量」与「全量重扫」这两小段，
	// 避免并发写入时重复计数、重扫与写入互相覆盖。下载/哈希等耗时部分不在锁内。
	putMu sync.Mutex
	used  atomic.Int64
	// lastScan 是上次全量扫描完成的时间（UnixNano），用于判断是否该重扫。
	lastScan atomic.Int64
	// 两次全量扫描的最小间隔；NewBlobs 之后不再修改（测试可按需缩短）。
	rescanEvery time.Duration
}

var blobKey = regexp.MustCompile(`^[0-9a-f]{128}$`)

func NewBlobs(dataDir string) (*Blobs, error) {
	dir := filepath.Join(dataDir, "blobs")
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o750); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s: %w", dir, err)
	}
	b := &Blobs{dir: dir, rescanEvery: defaultBlobRescanInterval}
	used, err := b.scanUsage()
	if err != nil {
		return nil, fmt.Errorf("统计已缓存 tarball 体积 %s: %w", dir, err)
	}
	b.used.Store(used)
	b.lastScan.Store(time.Now().UnixNano())
	return b, nil
}

// Usage 返回 blobs 目录当前已落盘 tarball 的总字节数（不含 tmp 下正在写入的文件）。
// 这是内存里的累计值；跨实例/外部改动要等 RefreshIfStale 或 Rescan 之后才反映出来。
func (b *Blobs) Usage() int64 { return b.used.Load() }

// Rescan 强制从磁盘重算用量并重置计数。测试与运维手动校正用；写路径上的周期重扫见
// RefreshIfStale。重扫与 Put 的落盘段互斥，因此不会把并发写入漏算或重算。
func (b *Blobs) Rescan() (int64, error) {
	b.putMu.Lock()
	defer b.putMu.Unlock()
	total, err := b.scanUsage()
	if err != nil {
		return b.used.Load(), err
	}
	b.used.Store(total)
	b.lastScan.Store(time.Now().UnixNano())
	return total, nil
}

// RefreshIfStale 返回当前用量；距上次全量扫描超过 rescanEvery 时先从磁盘重扫。
// 并发调用时只有一个会真正扫盘（用 lastScan 抢占），其余直接用当前计数；
// 重扫失败时保留旧计数（上限检查宁可继续用可能偏低的旧值，也不让请求 500）。
func (b *Blobs) RefreshIfStale() int64 {
	interval := b.rescanEvery
	if interval <= 0 {
		interval = defaultBlobRescanInterval
	}
	now := time.Now()
	for {
		last := b.lastScan.Load()
		if last != 0 && now.UnixNano()-last < int64(interval) {
			return b.used.Load()
		}
		if b.lastScan.CompareAndSwap(last, now.UnixNano()) {
			break
		}
	}
	if total, err := b.Rescan(); err == nil {
		return total
	}
	return b.used.Load()
}

// scanUsage 统计目录里已有 tarball 的总大小。重启后内存计数从磁盘重建，
// 否则每次重启上限都会从零重算、重启前的缓存不再占额度。
func (b *Blobs) scanUsage() (int64, error) {
	var total int64
	err := filepath.WalkDir(b.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// tmp 下是还没改名的半个文件，不算进已占用空间。
			if p != b.dir && d.Name() == "tmp" {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// Digest 是一个 tarball 的摘要：npm 的 integrity（sha512）与旧客户端用的 shasum（sha1）。
type Digest struct {
	Key       string // sha512 hex，也是存储文件名
	Integrity string // "sha512-<base64>"
	Shasum    string // sha1 hex
	Size      int64
}

var ErrTooLarge = errors.New("Tarball exceeds the size limit")

// Put 写入并返回摘要；超过 limit 字节时返回 ErrTooLarge。
func (b *Blobs) Put(r io.Reader, limit int64) (Digest, error) {
	tmp, err := os.CreateTemp(filepath.Join(b.dir, "tmp"), "upload-*")
	if err != nil {
		return Digest{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	h512, h1 := sha512.New(), sha1.New()
	n, err := io.Copy(io.MultiWriter(tmp, h512, h1), io.LimitReader(r, limit+1))
	if err != nil {
		return Digest{}, err
	}
	if n > limit {
		return Digest{}, ErrTooLarge
	}
	if err := tmp.Sync(); err != nil {
		return Digest{}, err
	}
	sum := h512.Sum(nil)
	d := Digest{
		Key:       hex.EncodeToString(sum),
		Integrity: "sha512-" + base64.StdEncoding.EncodeToString(sum),
		Shasum:    hex.EncodeToString(h1.Sum(nil)),
		Size:      n,
	}
	dst := b.path(d.Key)
	b.putMu.Lock()
	defer b.putMu.Unlock()
	if _, err := os.Stat(dst); err == nil {
		// 内容已存在（按内容寻址）：不重复占空间。
		return d, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return Digest{}, err
	}
	if err := tmp.Close(); err != nil {
		return Digest{}, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return Digest{}, err
	}
	b.used.Add(n)
	return d, nil
}

// Open 打开已存的 tarball。
func (b *Blobs) Open(key string) (*os.File, error) {
	if !blobKey.MatchString(key) {
		return nil, fmt.Errorf("Invalid blob key %q", key)
	}
	return os.Open(b.path(key))
}

func (b *Blobs) Has(key string) bool {
	if !blobKey.MatchString(key) {
		return false
	}
	_, err := os.Stat(b.path(key))
	return err == nil
}

func (b *Blobs) path(key string) string {
	return filepath.Join(b.dir, key[:2], key[2:4], key)
}

// CheckIntegrity 校验 npm 元数据里声明的 integrity / shasum 与实际内容一致。
// integrity 可能带多个哈希（空格分隔），只认 sha512；没有 sha512 时退回 shasum。
func CheckIntegrity(d Digest, integrity, shasum string) error {
	if integrity != "" {
		for _, part := range strings.Fields(integrity) {
			if strings.HasPrefix(part, "sha512-") {
				if part == d.Integrity {
					return nil
				}
				return fmt.Errorf("sha512 mismatch: declared %s, actual %s", part, d.Integrity)
			}
		}
	}
	if shasum != "" {
		if shasum == d.Shasum {
			return nil
		}
		return fmt.Errorf("shasum mismatch: declared %s, actual %s", shasum, d.Shasum)
	}
	if integrity != "" {
		// 只有 sha1 / sha256 形式的 integrity、也没有 shasum：极老的包，无法用 sha512 校验
		return errors.New("integrity contains no sha512 and shasum is missing")
	}
	return errors.New("Metadata has neither integrity nor shasum, so it cannot be verified")
}
