package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// 常驻云电脑的状态。none：还没有电脑或已被重置；waking：正在新建或从休眠恢复；
// running：开着（可能正在跑任务，也可能空闲等待休眠）；hibernating：正在休眠；hibernated：已休眠。
const (
	RuntimeComputerNone        = "none"
	RuntimeComputerWaking      = "waking"
	RuntimeComputerRunning     = "running"
	RuntimeComputerHibernating = "hibernating"
	RuntimeComputerHibernated  = "hibernated"
)

// 云电脑归谁：一位员工（他的所有个人机器人共用），或一个团队（部门，只给团队对话用）。
const (
	RuntimeComputerOwnerUser = "user"
	RuntimeComputerOwnerTeam = "team"
)

// ErrRuntimeComputerBusy：电脑正被另一个任务或操作占用（租约未到期）。
var ErrRuntimeComputerBusy = errors.New("runtime computer is busy")

// RuntimeComputerOwner 是电脑的所有者。员工电脑只有 UserID，团队电脑只有 Department。
type RuntimeComputerOwner struct {
	Kind       string
	UserID     int64
	Department string
}

func UserComputerOwner(userID int64) RuntimeComputerOwner {
	return RuntimeComputerOwner{Kind: RuntimeComputerOwnerUser, UserID: userID}
}

func TeamComputerOwner(department string) RuntimeComputerOwner {
	return RuntimeComputerOwner{Kind: RuntimeComputerOwnerTeam, Department: department}
}

// Valid：员工电脑要有员工，团队电脑要有部门；两者不能混用。
func (o RuntimeComputerOwner) Valid() bool {
	switch o.Kind {
	case RuntimeComputerOwnerUser:
		return o.UserID > 0 && o.Department == ""
	case RuntimeComputerOwnerTeam:
		return o.UserID == 0 && o.Department != ""
	}
	return false
}

func (o RuntimeComputerOwner) String() string {
	if o.Kind == RuntimeComputerOwnerTeam {
		return "team:" + o.Department
	}
	return "user:" + strconv.FormatInt(o.UserID, 10)
}

type RuntimeComputer struct {
	ID         int64  `json:"id"`
	OwnerKind  string `json:"owner_kind"`
	OwnerID    int64  `json:"owner_id,omitempty"`
	Department string `json:"department,omitempty"`
	// OwnerName 是员工的用户名（团队电脑为空）。
	OwnerName string `json:"owner_name,omitempty"`
	// BotID / BotName 是正在用（或最近一次用）这台电脑的机器人。
	BotID        int64      `json:"bot_id,omitempty"`
	BotName      string     `json:"bot_name,omitempty"`
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

func (c *RuntimeComputer) Owner() RuntimeComputerOwner {
	return RuntimeComputerOwner{Kind: c.OwnerKind, UserID: c.OwnerID, Department: c.Department}
}

const runtimeComputerColumns = `c.id, c.owner_kind, c.owner_user_id, c.owner_department, COALESCE(u.username,''), c.holder_bot_id, COALESCE(b.name,''),
	c.sandbox_id, c.creation_key, c.gateway_host, c.state, (c.lease_until IS NOT NULL AND c.lease_until>?), c.disk_bytes,
	c.last_used_at, c.hibernated_at, c.created_at, c.updated_at`

const runtimeComputerFrom = ` FROM runtime_computers c LEFT JOIN users u ON u.id=c.owner_user_id AND c.owner_kind='user'
	LEFT JOIN runtime_bots b ON b.id=c.holder_bot_id`

func scanRuntimeComputer(row interface{ Scan(...any) error }) (*RuntimeComputer, error) {
	var c RuntimeComputer
	err := row.Scan(&c.ID, &c.OwnerKind, &c.OwnerID, &c.Department, &c.OwnerName, &c.BotID, &c.BotName,
		&c.SandboxID, &c.CreationKey, &c.GatewayHost, &c.State, &c.Busy, &c.DiskBytes, &c.LastUsedAt, &c.HibernatedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// RuntimeComputerOf 读一个所有者的电脑；没有记录时返回 ErrNotFound（等同于 none）。
func (s *Store) RuntimeComputerOf(ctx context.Context, owner RuntimeComputerOwner) (*RuntimeComputer, error) {
	if !owner.Valid() {
		return nil, ErrNotFound
	}
	return scanRuntimeComputer(s.db.QueryRowContext(ctx, `SELECT `+runtimeComputerColumns+runtimeComputerFrom+`
		WHERE c.owner_kind=? AND c.owner_user_id=? AND c.owner_department=?`, now(), owner.Kind, owner.UserID, owner.Department))
}

// RuntimeComputer 按 ID 读一台电脑（管理员操作用）。
func (s *Store) RuntimeComputer(ctx context.Context, id int64) (*RuntimeComputer, error) {
	return scanRuntimeComputer(s.db.QueryRowContext(ctx, `SELECT `+runtimeComputerColumns+runtimeComputerFrom+` WHERE c.id=?`, now(), id))
}

const runtimeComputerPageSize = 100

// RuntimeComputers 分页列出有电脑的记录（管理员查看与回收用）。按电脑 ID 倒序翻页，跨请求稳定；
// before<=0 是第一页，返回的 next 为 0 表示没有下一页。
func (s *Store) RuntimeComputers(ctx context.Context, before int64) ([]RuntimeComputer, int64, error) {
	list, err := s.queryRuntimeComputers(ctx, `SELECT `+runtimeComputerColumns+runtimeComputerFrom+`
		WHERE c.state<>? AND (?<=0 OR c.id<?) ORDER BY c.id DESC LIMIT ?`, now(), RuntimeComputerNone, before, before, runtimeComputerPageSize+1)
	if len(list) > runtimeComputerPageSize {
		list = list[:runtimeComputerPageSize]
		return list, list[len(list)-1].ID, err
	}
	return list, 0, err
}

// IdleRuntimeComputers 返回该休眠或该回收的电脑：没有租约、开着且最后使用早于 idleBefore 的，
// 以及没有租约、已休眠且休眠早于 retainBefore 的（retainBefore 为零值时不回收）。
func (s *Store) IdleRuntimeComputers(ctx context.Context, idleBefore, retainBefore time.Time) ([]RuntimeComputer, error) {
	t := now()
	retain := sql.NullTime{Time: retainBefore, Valid: !retainBefore.IsZero()}
	return s.queryRuntimeComputers(ctx, `SELECT `+runtimeComputerColumns+runtimeComputerFrom+`
		WHERE (c.lease_until IS NULL OR c.lease_until<=?) AND (c.sandbox_id<>'' OR c.creation_key<>'') AND
		 ((c.state IN (?,?,?) AND c.last_used_at<?) OR (c.state=? AND ? IS NOT NULL AND c.hibernated_at<?))
		ORDER BY c.id LIMIT 100`,
		t, t, RuntimeComputerRunning, RuntimeComputerWaking, RuntimeComputerHibernating, idleBefore, RuntimeComputerHibernated, retain, retain)
}

func (s *Store) queryRuntimeComputers(ctx context.Context, q string, args ...any) ([]RuntimeComputer, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeComputer{}
	for rows.Next() {
		c, err := scanRuntimeComputer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// AcquireRuntimeComputer 为 holder 取得所有者电脑的独占租约，直到 until；botID 是要用这台电脑的机器人
// （手动操作传 0，不改记录里的机器人）。没有记录时先建一条 none。
// 租约被别人持有且未到期时返回 ErrRuntimeComputerBusy。到期的租约可以被接管（持有者崩溃的情况）。
func (s *Store) AcquireRuntimeComputer(ctx context.Context, owner RuntimeComputerOwner, holder string, botID int64, until time.Time) (*RuntimeComputer, error) {
	if !owner.Valid() {
		return nil, errors.New("invalid runtime computer owner")
	}
	t := now()
	if _, err := s.db.ExecContext(ctx, `INSERT IGNORE INTO runtime_computers (owner_kind, owner_user_id, owner_department, state, last_used_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)`, owner.Kind, owner.UserID, owner.Department, RuntimeComputerNone, t, t, t); err != nil {
		return nil, err
	}
	c, err := s.RuntimeComputerOf(ctx, owner)
	if err != nil {
		return nil, err
	}
	return s.AcquireRuntimeComputerByID(ctx, c.ID, holder, botID, until)
}

// AcquireRuntimeComputerByID 同 AcquireRuntimeComputer，按电脑 ID（电脑必须已有记录）。
func (s *Store) AcquireRuntimeComputerByID(ctx context.Context, id int64, holder string, botID int64, until time.Time) (*RuntimeComputer, error) {
	t := now()
	err := runtimeAffected(s.db.ExecContext(ctx, `UPDATE runtime_computers SET holder=?, lease_until=?, holder_bot_id=IF(?>0, ?, holder_bot_id)
		WHERE id=? AND (lease_until IS NULL OR lease_until<=? OR holder=?)`, holder, until, botID, botID, id, t, holder))
	if errors.Is(err, ErrConflict) {
		if _, err := s.RuntimeComputer(ctx, id); err != nil {
			return nil, err
		}
		return nil, ErrRuntimeComputerBusy
	}
	if err != nil {
		return nil, err
	}
	return s.RuntimeComputer(ctx, id)
}

// UpdateRuntimeComputer 在持有租约时写入电脑的状态。release 为真时同时释放租约。
// touched 为真时把 last_used_at 记成现在（空闲计时从这一刻重新开始）。租约已丢失时返回 ErrConflict。
func (s *Store) UpdateRuntimeComputer(ctx context.Context, c *RuntimeComputer, holder string, touched, release bool) error {
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
	q := `UPDATE runtime_computers SET sandbox_id=?, creation_key=?, gateway_host=?, state=?, disk_bytes=?, last_used_at=?, hibernated_at=?, updated_at=?`
	if release {
		q += `, holder='', lease_until=NULL`
	}
	err := runtimeAffected(s.db.ExecContext(ctx, q+` WHERE id=? AND holder=? AND lease_until>?`,
		c.SandboxID, c.CreationKey, c.GatewayHost, c.State, c.DiskBytes, c.LastUsedAt, c.HibernatedAt, t, c.ID, holder, t))
	if err == nil {
		c.UpdatedAt = t
	}
	return err
}

// ReleaseRuntimeComputer 只释放租约，不改状态（持有者什么也没做成时用）。
func (s *Store) ReleaseRuntimeComputer(ctx context.Context, id int64, holder string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runtime_computers SET holder='', lease_until=NULL WHERE id=? AND holder=?`, id, holder)
	return err
}

// RuntimeComputerOrphan 是迁移留下、还没删除的旧沙箱。SandboxID 为空时只有 CreationKey（旧记录新建到一半中断），
// 要先按它向 provider 找到沙箱。
type RuntimeComputerOrphan struct {
	ID          int64
	SandboxID   string
	CreationKey string
}

// RuntimeComputerOrphans 返回迁移留下、还没删除的旧沙箱。
func (s *Store) RuntimeComputerOrphans(ctx context.Context, limit int) ([]RuntimeComputerOrphan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, sandbox_id, creation_key FROM runtime_computer_orphans ORDER BY created_at, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeComputerOrphan{}
	for rows.Next() {
		var o RuntimeComputerOrphan
		if err := rows.Scan(&o.ID, &o.SandboxID, &o.CreationKey); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// DeleteRuntimeComputerOrphan 在沙箱删除（或确认从没建成）后去掉它的回收记录。
func (s *Store) DeleteRuntimeComputerOrphan(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM runtime_computer_orphans WHERE id=?`, id)
	return err
}

// RuntimeComputerBotCount 是共用一位员工电脑的个人机器人数（以本人身份运行的机器人）。
func (s *Store) RuntimeComputerBotCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_bots WHERE owner_id=? AND run_as_id=owner_id`, userID).Scan(&n)
	return n, err
}
