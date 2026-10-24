package main

// `siwu backup`：外部备份工具（docker-db-auto-backup）经 docker exec 调用的导出
// 子命令，契约见 .github 仓 docs/backup.md。stdout 只承载备份数据，日志一律走
// stderr；分发点在 main() 最前面，早于任何存储初始化——导出进程不迁移、不建
// 数据目录、不产空库。

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Felix2yu/siwu/internal/store"
)

// runBackup 执行导出。stdout 由调用方传入（main 里是进程真实 stdout，测试里
// 可以是 buffer），数据只写它，人话只走 stderr。
func runBackup(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("siwu backup", flag.ContinueOnError)
	dataDir := flags.String("data", backupDataDir(), "数据目录")
	format := flags.String("format", "db", "导出格式：db（SQLite 一致性快照）或 zip（快照 + secret.key）")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	st, err := store.OpenForExport(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	switch *format {
	case "db":
		return st.WriteSQLiteSnapshot(stdout)
	case "zip":
		n, err := st.WriteExportZIP(stdout)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[backup] zip 完成：库快照 + %d 个库外文件\n", n)
		return nil
	default:
		return fmt.Errorf("未知格式 %q（可选 db / zip）", *format)
	}
}

// backupDataDir 与 serve 的默认口径对齐，另认基镜烘入的 APP_DATA_DIR：
// docker exec 不走 ENTRYPOINT，容器里 exec `siwu backup` 不带 --data 也能找对位置。
func backupDataDir() string {
	if v := os.Getenv("SIWU_DATA_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("APP_DATA_DIR"); v != "" {
		return v
	}
	return "./data"
}
