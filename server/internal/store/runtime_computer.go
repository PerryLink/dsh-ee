package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// 机器人常驻云电脑的状态。none：还没有电脑或已被重置；waking：正在新建或从休眠恢复；
// running：开着（可能正在跑任务，也可能空闲等待休眠）；hibernating：正在休眠；hibernated：已休眠。
const (
	RuntimeComputerNone        = "none"
	RuntimeComputerWaking      = "waking"
	RuntimeComputerRunning     = "running"
	RuntimeComputerHibernating = "hibernating"
	RuntimeComputerHibernated  = "hibernated"
)

// ErrRuntimeComputerBusy：电脑正被另一个任务或操作占用（租约未到期）。
var ErrRuntimeComputerBusy = errors.New("runtime bot computer is busy")

type RuntimeBotComputer struct {
	BotID        int64      `json:"bot_id"`
	BotName      string     `json:"bot_name,omitempty"`
	OwnerID      int64      `json:"owner_id,omitempty"`
	SandboxID    string     `json:"-"`
	CreationKey  string     `json:"-"`
	GatewayHost  string     `json:"-"`
	State        string     `json:"state"`
	Busy         bool       `json:"busy"`
	DiskBytes    int64      `json:"disk_bytes"`
	LastUsedAt   time.Time  `json:"last_used_at"`
	HibernatedAt *time.Time `json:"hibernated_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

const runtimeComputerColumns = `c.bot_id, COALESCE(b.name,''), COALESCE(b.owner_id,0), c.sandbox_id, c.creation_key, c.gateway_host, c.state,
	(c.lease_until IS NOT NULL AND c.lease_until>?), c.disk_bytes, c.last_used_at, c.hibernated_at, c.created_at, c.updated_at`

func scanRuntimeComputer(row interface{ Scan(...any) error }) (*RuntimeBotComputer, error) {
	var c RuntimeBotComputer
	err := row.Scan(&c.BotID, &c.BotName, &c.OwnerID, &c.SandboxID, &c.CreationKey, &c.GatewayHost, &c.State, &c.Busy, &c.DiskBytes, &c.LastUsedAt, &c.HibernatedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// RuntimeBotComputer 读一个机器人的电脑；没有记录时返回 ErrNotFound（等同于 none）。
func (s *Store) RuntimeBotComputer(ctx context.Context, botID int64) (*RuntimeBotComputer, error) {
	return scanRuntimeComputer(s.db.QueryRowContext(ctx, `SELECT `+runtimeComputerColumns+` FROM runtime_bot_computers c
		LEFT JOIN runtime_bots b ON b.id=c.bot_id WHERE c.bot_id=?`, now(), botID))
}

const runtimeComputerPageSize = 100

// RuntimeBotComputers pages through inventory by bot ID, which is stable across runs.
func (s *Store) RuntimeBotComputers(ctx context.Context, before int64) ([]RuntimeBotComputer, int64, error) {
	list, err := s.queryRuntimeComputers(ctx, `SELECT `+runtimeComputerColumns+` FROM runtime_bot_computers c
		LEFT JOIN runtime_bots b ON b.id=c.bot_id WHERE c.state<>? AND (?<=0 OR c.bot_id<?)
		ORDER BY c.bot_id DESC LIMIT ?`, now(), RuntimeComputerNone, before, before, runtimeComputerPageSize+1)
	if len(list) > runtimeComputerPageSize {
		list = list[:runtimeComputerPageSize]
		return list, list[len(list)-1].BotID, err
	}
	return list, 0, err
}

// IdleRuntimeBotComputers 返回该休眠或该回收的电脑：没有租约、开着且最后使用早于 idleBefore 的，
// 以及没有租约、已休眠且休眠早于 retainBefore 的（retainBefore 为零值时不回收）。
func (s *Store) IdleRuntimeBotComputers(ctx context.Context, idleBefore, retainBefore time.Time) ([]RuntimeBotComputer, error) {
	t := now()
	retain := sql.NullTime{Time: retainBefore, Valid: !retainBefore.IsZero()}
	return s.queryRuntimeComputers(ctx, `SELECT `+runtimeComputerColumns+` FROM runtime_bot_computers c
		LEFT JOIN runtime_bots b ON b.id=c.bot_id
		WHERE (c.lease_until IS NULL OR c.lease_until<=?) AND (c.sandbox_id<>'' OR c.creation_key<>'') AND
		 ((c.state IN (?,?,?) AND c.last_used_at<?) OR (c.state=? AND ? IS NOT NULL AND c.hibernated_at<?))
		ORDER BY c.bot_id LIMIT 100`,
		t, t, RuntimeComputerRunning, RuntimeComputerWaking, RuntimeComputerHibernating, idleBefore, RuntimeComputerHibernated, retain, retain)
}

func (s *Store) queryRuntimeComputers(ctx context.Context, q string, args ...any) ([]RuntimeBotComputer, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeBotComputer{}
	for rows.Next() {
		c, err := scanRuntimeComputer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// AcquireRuntimeBotComputer 为 holder 取得机器人电脑的独占租约，直到 until。没有记录时先建一条 none。
// 租约被别人持有且未到期时返回 ErrRuntimeComputerBusy。到期的租约可以被接管（持有者崩溃的情况）。
func (s *Store) AcquireRuntimeBotComputer(ctx context.Context, botID int64, holder string, until time.Time) (*RuntimeBotComputer, error) {
	t := now()
	if _, err := s.db.ExecContext(ctx, `INSERT IGNORE INTO runtime_bot_computers (bot_id, state, last_used_at, created_at, updated_at) VALUES (?,?,?,?,?)`,
		botID, RuntimeComputerNone, t, t, t); err != nil {
		return nil, err
	}
	err := runtimeAffected(s.db.ExecContext(ctx, `UPDATE runtime_bot_computers SET holder=?, lease_until=?
		WHERE bot_id=? AND (lease_until IS NULL OR lease_until<=? OR holder=?)`, holder, until, botID, t, holder))
	if errors.Is(err, ErrConflict) {
		return nil, ErrRuntimeComputerBusy
	}
	if err != nil {
		return nil, err
	}
	return s.RuntimeBotComputer(ctx, botID)
}

// UpdateRuntimeBotComputer 在持有租约时写入电脑的状态。release 为真时同时释放租约。
// touched 为真时把 last_used_at 记成现在（空闲计时从这一刻重新开始）。租约已丢失时返回 ErrConflict。
func (s *Store) UpdateRuntimeBotComputer(ctx context.Context, c *RuntimeBotComputer, holder string, touched, release bool) error {
	t := now()
	if touched {
		c.LastUsedAt = t
	}
	if c.State == RuntimeComputerHibernated && c.HibernatedAt == nil {
		c.HibernatedAt = &t
	}
	if c.State != RuntimeComputerHibernated {
		c.HibernatedAt = nil
	}
	if c.SandboxID == "" && c.CreationKey == "" && (release || c.State != RuntimeComputerWaking) {
		c.State, c.DiskBytes = RuntimeComputerNone, 0
		c.GatewayHost = ""
	}
	q := `UPDATE runtime_bot_computers SET sandbox_id=?, creation_key=?, gateway_host=?, state=?, disk_bytes=?, last_used_at=?, hibernated_at=?, updated_at=?`
	if release {
		q += `, holder='', lease_until=NULL`
	}
	err := runtimeAffected(s.db.ExecContext(ctx, q+` WHERE bot_id=? AND holder=? AND lease_until>?`,
		c.SandboxID, c.CreationKey, c.GatewayHost, c.State, c.DiskBytes, c.LastUsedAt, c.HibernatedAt, t, c.BotID, holder, t))
	if err == nil {
		c.UpdatedAt = t
	}
	return err
}

// ReleaseRuntimeBotComputer 只释放租约，不改状态（持有者什么也没做成时用）。
func (s *Store) ReleaseRuntimeBotComputer(ctx context.Context, botID int64, holder string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runtime_bot_computers SET holder='', lease_until=NULL WHERE bot_id=? AND holder=?`, botID, holder)
	return err
}
