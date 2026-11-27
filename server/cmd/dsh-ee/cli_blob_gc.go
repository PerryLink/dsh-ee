package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hikariming/dsh-ee/server/internal/registry"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

func cliBlobGC(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("blob-gc", flag.ContinueOnError)
	flags.SetOutput(stderr)
	remove := flags.Bool("delete", false, tr("删除未引用的 blob；省略时只列出", "delete unreferenced blobs; omit to list only"))
	force := flags.Bool("force", false, tr(
		"数据库 schema 比当前二进制新时也删除（危险：可能误删新版本引用的 blob）",
		"delete even when the database schema is newer than this binary (dangerous: may delete blobs still referenced)"))
	usage := tr(
		"用法：dsh-ee blob-gc [--delete] [--force]\n"+
			"请设置 DSH_EE_DSN 和 DSH_EE_DATA_DIR，并在运行前停止所有写入进程。\n"+
			"--delete 前会检查数据库 schema 是否比当前二进制新（回滚到旧版本后连新库的典型形态）："+
			"更新时拒绝删除，避免旧二进制的引用表清单漏掉新表而误删；只有 --force 能跳过检查。",
		"Usage: dsh-ee blob-gc [--delete] [--force]\n"+
			"Set DSH_EE_DSN and DSH_EE_DATA_DIR, and stop all writers before running.\n"+
			"Before --delete it checks whether the database schema is newer than this binary "+
			"(what you get after rolling back while the database stays migrated): it refuses in that case, "+
			"so an old binary's reference table list cannot silently miss new tables; only --force skips the check.",
	)
	flags.Usage = func() {}
	if err := flags.Parse(args); err == flag.ErrHelp {
		fmt.Fprintln(stdout, usage)
		return 0
	} else if err != nil {
		return 2
	}
	// --force 只影响 --delete 前的闸门；单独写出来容易让人以为它本身会删除。
	if *force && !*remove {
		fmt.Fprintln(stderr, tr("--force 只和 --delete 一起用；要删除请显式加 --delete。",
			"--force only works together with --delete; add --delete to delete."))
		return 2
	}
	dsn, dataDir := os.Getenv("DSH_EE_DSN"), os.Getenv("DSH_EE_DATA_DIR")
	if flags.NArg() != 0 || dsn == "" || dataDir == "" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.OpenExisting(ctx, dsn)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer st.Close()
	if *remove && !*force {
		if err := checkBlobGCSchema(ctx, st); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	var count int
	var bytes int64
	err = registry.CollectBlobs(ctx, dataDir, st, *remove, func(key string, size int64) error {
		count++
		bytes += size
		_, err := fmt.Fprintf(stdout, "%s\t%d\n", key, size)
		return err
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *remove {
		fmt.Fprintf(stdout, tr("已删除 %d 个 blob，共 %d 字节。\n", "Deleted %d blobs (%d bytes).\n"), count, bytes)
	} else {
		fmt.Fprintf(stdout, tr("预览：%d 个未引用的 blob，共 %d 字节；未删除文件。\n", "Dry run: %d unreferenced blobs (%d bytes); no files deleted.\n"), count, bytes)
	}
	return 0
}

// checkBlobGCSchema 在 --delete 前确认数据库 schema 不比当前二进制新。
//
// 回滚场景：数据库已经被更高版本迁移过，而这里跑的是旧二进制。如果新版本新增了
// blob_key 引用表，旧二进制的 blobRefTables 清单（store/blobs.go）不认识它，扫不到
// 那些引用，就会把新表引用的 blob 当孤儿删掉——删掉的是新版本还在用的文件。
// 因此数据库比二进制新时拒绝删除；操作者确认数据库里没有新的 blob 引用表后才能 --force。
func checkBlobGCSchema(ctx context.Context, st *store.Store) error {
	state, err := st.MigrationStatus(ctx)
	if err != nil {
		return fmt.Errorf(tr(
			"读不了数据库迁移版本（schema_migrations），为安全起见拒绝 --delete：%w\n"+
				"确认数据库不可读和 blob 无关时才加 --force。",
			"cannot read the database migration state (schema_migrations); refusing --delete to be safe: %w\n"+
				"add --force only if the unreadable database is unrelated to blobs."), err)
	}
	if len(state.Ahead) == 0 {
		return nil
	}
	return fmt.Errorf(tr(
		"数据库 schema 比当前二进制新：数据库已应用 %s，本二进制内置最新迁移是 %s。\n"+
			"回滚到旧版本后会出现这种状态：旧二进制的 blob 引用表清单可能不认识新版本新增的表，"+
			"继续删除会误删新引用指向的文件，所以拒绝执行。\n"+
			"请改用与服务端同版本的 dsh-ee 执行；确认数据库里没有新的 blob 引用表后才加 --force。",
		"database schema is newer than this binary: applied %s, latest embedded migration is %s.\n"+
			"After rolling back, this binary's blob reference table list may not know tables added by the "+
			"newer version, so deleting would remove blobs that are still referenced; refusing.\n"+
			"Use the dsh-ee binary that matches the server; add --force only if you know no new blob "+
			"reference table exists."),
		strings.Join(state.Ahead, ", "), state.Latest)
}
