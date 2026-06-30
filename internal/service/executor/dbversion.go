package executor

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gongdan/internal/gameserver"

	_ "github.com/go-sql-driver/mysql"
)

// buildPacketNameCmd 读取换包后落在服目录的 packetName.txt(含包名/版本)。
func buildPacketNameCmd(id int) string {
	return fmt.Sprintf("cat /export/server/server_%d/packetName.txt", id)
}

// queryDBVersion 用 database/sql 连库查 DBVERSION。密码只在内存 DSN,不进命令/日志。
func queryDBVersion(conn gameserver.DBConn, id int) (int, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/ptdb_%d?timeout=5s&readTimeout=5s",
		conn.User, conn.Pwd, conn.IP, conn.Port, id)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return 0, fmt.Errorf("打开DB连接: %w", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ver int
	err = db.QueryRowContext(ctx,
		"select nVal from t_general_set where sKey=?", "DBVERSION").Scan(&ver)
	if err != nil {
		return 0, fmt.Errorf("查询DBVERSION: %w", err)
	}
	return ver, nil
}
