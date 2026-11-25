package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/registry"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

func cliBlobGC(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("blob-gc", flag.ContinueOnError)
	flags.SetOutput(stderr)
	remove := flags.Bool("delete", false, tr("删除未引用的 blob；省略时只列出", "delete unreferenced blobs; omit to list only"))
	force := flags.Bool("force", false, tr(
		"越过「数据库与数据目录不配对」的保护；确认是同一套实例再用",
		"bypass the \"database and data dir do not look paired\" guard; only after confirming"))
	minAge := flags.Duration("min-age", 24*time.Hour, tr(
		"跳过最近修改的文件；0 表示不跳过",
		"skip files modified more recently than this; 0 disables"))
	usage := tr(
		`用法：dsh-ee blob-gc [--delete] [--force] [--min-age 时长]
请设置 DSH_EE_DSN 和 DSH_EE_DATA_DIR（绝对路径），并在运行前停止所有写入进程。
  --delete    删除未引用的 blob；省略时只列出
  --min-age   跳过最近修改的文件，默认 24h；0 表示不跳过
  --force     越过「数据库与数据目录不配对」的保护；确认是同一套实例再用
退出码：0 成功；1 配对闸门拒绝或运行错误；2 用法/配置错误；3 有文件删除失败。`,
		`Usage: dsh-ee blob-gc [--delete] [--force] [--min-age duration]
Set DSH_EE_DSN and DSH_EE_DATA_DIR (an absolute path) and stop all writers before running.
  --delete    delete unreferenced blobs; omit to list only
  --min-age   skip files modified more recently than this, default 24h; 0 disables
  --force     bypass the "database and data dir do not look paired" guard; only after confirming
Exit codes: 0 success; 1 pairing gate refused or runtime error; 2 usage/config error; 3 some deletes failed.`)
	flags.Usage = func() {}
	if err := flags.Parse(args); err == flag.ErrHelp {
		fmt.Fprintln(stdout, usage)
		return 0
	} else if err != nil {
		return 2
	}
	if *minAge < 0 {
		fmt.Fprintln(stderr, tr("--min-age 不能为负数", "--min-age must not be negative"))
		return 2
	}
	dsn, dataDir := os.Getenv("DSH_EE_DSN"), os.Getenv("DSH_EE_DATA_DIR")
	if flags.NArg() != 0 || dsn == "" || dataDir == "" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	// 相对路径会随当前目录指向另一套数据目录，是 C1 类事故的入口之一：
	// 连库之前就拒绝，避免「连对库、删错目录」或反过来。
	if !filepath.IsAbs(dataDir) {
		abs, err := filepath.Abs(dataDir)
		if err != nil {
			fmt.Fprintf(stderr, tr("解析 DSH_EE_DATA_DIR %q：%v\n", "resolve DSH_EE_DATA_DIR %q: %v\n"), dataDir, err)
			return 2
		}
		if !*force {
			fmt.Fprintf(stderr, tr(
				"DSH_EE_DATA_DIR 必须是绝对路径（当前 %q，按当前目录会解析成 %q）；确认目录无误后可加 --force 继续\n",
				"DSH_EE_DATA_DIR must be an absolute path (got %q, which resolves to %q); add --force to continue after confirming the directory\n"), dataDir, abs)
			return 2
		}
		fmt.Fprintf(stderr, tr("警告：DSH_EE_DATA_DIR 是相对路径 %q，按当前目录解析为 %q\n",
			"warning: DSH_EE_DATA_DIR is relative (%q), resolved against the current directory to %q\n"), dataDir, abs)
		dataDir = abs
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.OpenExisting(ctx, dsn)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer st.Close()
	scan, err := registry.ScanBlobs(ctx, dataDir, st, registry.GCOptions{Remove: *remove, MinAge: *minAge, Force: *force})
	if scan != nil {
		defer scan.Close()
		printBlobGCScan(stdout, scan)
	}
	if scan == nil {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	if scan.Gate != nil {
		detail := blobGCGateText(scan)
		switch {
		case err != nil:
			fmt.Fprintf(stderr, tr("拒绝删除：数据库与数据目录疑似不配对：%s。\n",
				"refusing to delete: the database and data dir look unpaired: %s.\n"), detail)
			fmt.Fprintln(stderr, tr("如果确认 DSH_EE_DSN 与 DSH_EE_DATA_DIR 指向同一套实例，加 --force 重跑。",
				"If DSH_EE_DSN and DSH_EE_DATA_DIR do point to the same instance, rerun with --force."))
			return 1
		case *remove && *force:
			fmt.Fprintf(stderr, tr("警告：已用 --force 越过配对闸门（%s），仍会删除候选。\n",
				"warning: --force bypassed the pairing gate (%s); candidates will still be deleted.\n"), detail)
		default:
			fmt.Fprintf(stderr, tr("警告：数据库与数据目录疑似不配对（%s）；--delete 会被拒绝。\n",
				"warning: the database and data dir look unpaired (%s); --delete will be refused.\n"), detail)
		}
	}
	var total int64
	for _, c := range scan.Candidates {
		total += c.Size
		fmt.Fprintf(stdout, "%s\t%d\n", c.Key, c.Size)
	}
	if !*remove {
		fmt.Fprintf(stdout, tr("预览：%d 个可删除的未引用 blob，共 %d 字节；未删除任何文件。\n",
			"Dry run: %d unreferenced blobs (%d bytes) would be deleted; no files deleted.\n"), len(scan.Candidates), total)
		return 0
	}
	res := scan.Delete(ctx)
	for _, e := range res.Errors {
		fmt.Fprintf(stderr, "%s\t%s\n", e.Key, e.Err)
	}
	fmt.Fprintf(stdout, tr("删除完成：成功 %d 个，失败 %d 个，因修改时间太新跳过 %d 个。\n",
		"Delete finished: %d deleted, %d failed, %d skipped as too new.\n"), res.Deleted, res.Failed, scan.TooNew)
	if res.Failed > 0 {
		return 3
	}
	return 0
}

// printBlobGCScan 打印本次操作的目标与计数：先让人核对「库和目录是不是同一套实例」，
// 再决定要不要看候选、要不要删。
func printBlobGCScan(w io.Writer, scan *registry.BlobScan) {
	fmt.Fprintf(w, tr("目标目录：%s\n", "blobs dir: %s\n"), scan.BlobsDir)
	fmt.Fprintf(w, tr("数据库引用 blob：%d 个\n", "blobs referenced by the database: %d\n"), scan.Referenced)
	fmt.Fprintf(w, tr("扫描 blob 文件：%d 个\n", "blob files scanned: %d\n"), scan.Scanned)
	fmt.Fprintf(w, tr("未引用候选：%d 个（因修改时间太新跳过 %d 个）\n",
		"unreferenced candidates: %d (skipped %d as too new)\n"), len(scan.Candidates), scan.TooNew)
}

// blobGCGateText 把配对闸门结论翻成一句人话。
func blobGCGateText(scan *registry.BlobScan) string {
	gate := scan.Gate
	switch gate.Kind {
	case registry.GateNoReferences:
		return tr(
			fmt.Sprintf("数据库里没有任何 blob 引用，数据目录里却有 %d 个候选文件", len(scan.Candidates)),
			fmt.Sprintf("the database has no blob references at all, yet the data dir has %d candidate files", len(scan.Candidates)))
	case registry.GateUnpaired:
		return tr(
			fmt.Sprintf("抽样的 %d 个引用 blob 里有 %d 个不在数据目录里", gate.Sampled, gate.SampleMissing),
			fmt.Sprintf("%d of %d sampled referenced blobs are missing from the data dir", gate.SampleMissing, gate.Sampled))
	}
	return string(gate.Kind)
}
