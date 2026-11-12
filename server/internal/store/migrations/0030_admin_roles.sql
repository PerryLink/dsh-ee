-- 扩展第 4 批（docs/access-control.md §3、docs/agent-dev.md §6.2）：配置变更请求、恢复账号。

-- 变更请求：权限配置（角色、字段、规则、管理约束）的修改先提交，由另一位管理员批准后才生效；
-- 给人授管理角色在开了双人批准时也走这里。before 是生效前的配置，用来回滚。
CREATE TABLE change_requests (
  id          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  -- config：整份权限配置；user_roles：给某人改角色
  kind        VARCHAR(16)  NOT NULL,
  title       VARCHAR(200) NOT NULL,
  payload     MEDIUMTEXT   NOT NULL,
  before_snap MEDIUMTEXT   NULL,
  -- pending / applied / rejected / failed
  status      VARCHAR(16)  NOT NULL DEFAULT 'pending',
  note        VARCHAR(1000) NOT NULL DEFAULT '',
  error       VARCHAR(1000) NOT NULL DEFAULT '',
  created_by  BIGINT       NOT NULL,
  -- 通过配置令牌提交的（Agent 做的）
  via_token   TINYINT      NOT NULL DEFAULT 0,
  decided_by  BIGINT       NULL,
  created_at  DATETIME(3)  NOT NULL,
  decided_at  DATETIME(3)  NULL,
  KEY idx_change_requests_status (status, id)
);

-- 应急恢复账号（dsh-ee admin recover）：1 小时后自动停用。
CREATE TABLE recovery_accounts (
  user_id     BIGINT       NOT NULL PRIMARY KEY,
  expires_at  DATETIME(3)  NOT NULL,
  created_at  DATETIME(3)  NOT NULL
);
