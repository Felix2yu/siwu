package store

// 备份导出的数据面：`siwu backup` 子命令（cmd 层）只做分发，真正干活在这里。
// 契约见 .github 仓 docs/backup.md——stdout 只承载备份数据，退出码失败非 0。

import (
	"archive/zip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	// DBFileName 是主库文件名，与 Open 的调用方共用一份约定。
	DBFileName = "siwu.db"
	// SecretKeyFileName 是库外密钥文件（secretbox 生成）。通知目标 URL 用它
	// 加密落库，恢复库而不带它，通知凭据就解不开了——zip 形态专门带上它。
	SecretKeyFileName = "secret.key"
)

// OpenForExport 以「只导出不改动」的方式打开数据目录里的数据库。
// 刻意不走 Open：不建目录、不迁移、不设 journal_mode / foreign_keys（导出连接
// 不回写库头，已有的 WAL 数据照常读取）；数据文件不存在直接报错——
// 导出进程绝不能把「路径配错」变成「凭空造一个空库」。
func OpenForExport(dataDir string) (*Store, error) {
	path := filepath.Join(dataDir, DBFileName)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("数据文件不可用: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1) // 导出是顺序读，一条连接足够，也少开一份 -shm 句柄
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	return &Store{db: db, path: path, now: time.Now}, nil
}

// WriteSQLiteSnapshot 把数据库以 SQLite 自身的一致性快照写到 w。
//
// 用 VACUUM INTO 而不是复制文件：热拷贝正在被写入的 db/-wal/-shm 不是原子操作，
// 可能拷出损坏副本或漏掉 WAL 里已提交的数据；VACUUM INTO 由 SQLite 在事务里
// 生成一份自洽快照，WAL 模式下也不阻塞写入。恢复即把文件放回数据目录
// （delete 日志模式，应用启动自行切回 WAL，不要手动转）。
//
// 快照先落临时目录再流式输出：VACUUM INTO 要求目标不存在，且这样无论调用方
// 读到一半是否断开，都不会在数据目录里留下半成品。
func (s *Store) WriteSQLiteSnapshot(w io.Writer) error {
	dir, err := os.MkdirTemp("", "siwu-snapshot-")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(dir)

	target := filepath.Join(dir, DBFileName)
	if err := s.vacuumInto(target); err != nil {
		return err
	}
	return streamFile(target, w)
}

// WriteExportZIP 把「库快照 + secret.key」打成 zip 流式写到 w，返回打包的
// 库外文件个数。siwu 的库外状态只有密钥一份，一份产物即可还原全部；
// 配合外部备份工具时需声明 BACKUP_SQLITE_EXT=zip。
func (s *Store) WriteExportZIP(w io.Writer) (int, error) {
	dir, err := os.MkdirTemp("", "siwu-zip-")
	if err != nil {
		return 0, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(dir)

	snap := filepath.Join(dir, DBFileName)
	if err := s.vacuumInto(snap); err != nil {
		return 0, err
	}

	zw := zip.NewWriter(w)
	if err := zipOneFile(zw, DBFileName, snap); err != nil {
		_ = zw.Close() // 尽力写完中央目录，让半截包在解包时还能报出前缀
		return 0, err
	}
	n, err := zipOneFileIfExists(zw, SecretKeyFileName, filepath.Join(s.DataDir(), SecretKeyFileName))
	if err != nil {
		_ = zw.Close()
		return n, err
	}
	if err := zw.Close(); err != nil {
		return n, fmt.Errorf("完成压缩包失败: %w", err)
	}
	return n, nil
}

// vacuumInto 用 VACUUM INTO 把当前库导出为磁盘上的一个自洽快照文件（目标须不存在）。
func (s *Store) vacuumInto(target string) error {
	if _, err := s.db.Exec("VACUUM INTO ?", target); err != nil {
		return fmt.Errorf("导出数据库快照失败: %w", err)
	}
	return nil
}

// DataDir 返回数据库所在的数据目录（OpenForExport 时代指数据根）。
func (s *Store) DataDir() string { return filepath.Dir(s.path) }

// streamFile 把整个文件拷进 w。
func streamFile(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取快照失败: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("写出快照失败: %w", err)
	}
	return nil
}

// zipOneFile 把磁盘上的一个文件作为指定条目写进压缩包。
func zipOneFile(zw *zip.Writer, name, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", name, err)
	}
	defer f.Close()
	hw, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("创建压缩包条目 %s 失败: %w", name, err)
	}
	if _, err := io.Copy(hw, f); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", name, err)
	}
	return nil
}

// zipOneFileIfExists 同 zipOneFile，但文件不存在时跳过（返回 0）而不报错——
// 库存在而密钥尚未生成的部署虽然少见，但 zip 里少一个条目仍可恢复库本身。
func zipOneFileIfExists(zw *zip.Writer, name, src string) (int, error) {
	if err := zipOneFile(zw, name, src); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	return 1, nil
}
