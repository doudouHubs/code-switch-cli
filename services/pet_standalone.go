package services

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/daodao97/xgo/xdb"
)

// InitPetStandaloneDatabase 初始化独立宠物进程需要的最小数据库。
// 默认沿用主程序的 app.db，让主程序设置页和独立桌宠共享同一份宠物状态；
// 通过 CODESWITCH_PET_DB_PATH 可在隔离测试或未来完全拆分时指定独立文件。
func InitPetStandaloneDatabase() (*sql.DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home: %w", err)
	}
	path := strings.TrimSpace(os.Getenv("CODESWITCH_PET_DB_PATH"))
	if path == "" {
		path = filepath.Join(home, ".code-switch", "app.db")
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create pet database directory: %w", err)
	}
	if err := xdb.Inits([]xdb.Config{{Name: "default", Driver: "sqlite", DSN: path + "?cache=shared&mode=rwc"}}); err != nil {
		return nil, fmt.Errorf("initialize pet database: %w", err)
	}
	db, err := xdb.DB("default")
	if err != nil {
		return nil, fmt.Errorf("open pet database: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 30000"); err != nil {
		return nil, fmt.Errorf("set pet database busy timeout: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return nil, fmt.Errorf("set pet database journal mode: %w", err)
	}
	if err := EnsurePetSchema(db); err != nil {
		return nil, err
	}
	return db, nil
}
