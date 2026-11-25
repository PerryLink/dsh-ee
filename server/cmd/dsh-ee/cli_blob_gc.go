package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/hikariming/dsh-ee/server/internal/registry"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

func cliBlobGC(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("blob-gc", flag.ContinueOnError)
	flags.SetOutput(stderr)
	remove := flags.Bool("delete", false, tr("删除未引用的 blob；省略时只列出", "delete unreferenced blobs; omit to list only"))
	usage := tr(
		"用法：dsh-ee blob-gc [--delete]\n请设置 DSH_EE_DSN 和 DSH_EE_DATA_DIR，并在运行前停止所有写入进程。",
		"Usage: dsh-ee blob-gc [--delete]\nSet DSH_EE_DSN and DSH_EE_DATA_DIR, and stop all writers before running.",
	)
	flags.Usage = func() {}
	if err := flags.Parse(args); err == flag.ErrHelp {
		fmt.Fprintln(stdout, usage)
		return 0
	} else if err != nil {
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
