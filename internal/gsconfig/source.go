package gsconfig

import (
	"strconv"
	"strings"

	"gongdan/internal/gameserver"
)

// ServerSource 把配置库行按列名映射为 gameserver 视图,实现 gameserver.Source。
type ServerSource struct{ store *Store }

func NewServerSource(store *Store) *ServerSource { return &ServerSource{store: store} }

// Servers 返回全部服(不含任何凭据)。
func (s *ServerSource) Servers() ([]gameserver.Server, error) {
	rows, err := s.store.AllRows()
	if err != nil {
		return nil, err
	}
	out := make([]gameserver.Server, 0, len(rows))
	for _, r := range rows {
		id, _ := strconv.Atoi(strings.TrimSpace(r["Id"]))
		wt, _ := strconv.Atoi(strings.TrimSpace(r["WorldType"]))
		rw, _ := strconv.Atoi(strings.TrimSpace(r["RealWorldID"]))
		bw, _ := strconv.Atoi(strings.TrimSpace(r["BattleWorldID"]))
		// 名称优先用 Desc 列(如「流云洲43服」);Desc 为空才回退 WorldName(如「Z43」)。
		name := strings.TrimSpace(r["Desc"])
		if name == "" {
			name = r["WorldName"]
		}
		out = append(out, gameserver.Server{
			ID:            id,
			Name:          name,
			Desc:          name, // 分组(GroupName)也用它,取出「流云洲」这种中文前缀
			WorldType:     wt,
			RealWorldID:   rw,
			BattleWorldID: bw,
			IP:            r["SelfPublicIp"],
			OutIP:         r["RealSelfPublicIp"],
		})
	}
	return out, nil
}

// DBConns 返回 服ID -> MySQL 连接(含密码,仅执行器内部用)。
func (s *ServerSource) DBConns() (map[int]gameserver.DBConn, error) {
	rows, err := s.store.AllRows()
	if err != nil {
		return nil, err
	}
	out := make(map[int]gameserver.DBConn, len(rows))
	for _, r := range rows {
		id, err := strconv.Atoi(strings.TrimSpace(r["Id"]))
		if err != nil {
			continue
		}
		out[id] = gameserver.DBConn{
			IP:   r["MySqlIp"],
			Port: r["MySqlPort"],
			User: r["DataBaseUser"],
			Pwd:  r["DataBasePsw"],
		}
	}
	return out, nil
}
