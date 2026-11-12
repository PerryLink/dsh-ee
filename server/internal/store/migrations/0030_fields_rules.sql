-- 扩展第 3 批（docs/access-control.md §4～§5）：扩展字段和按字段的访问规则。

-- 字段定义。扩展带进来的字段键是「扩展 id.键」，source 记是哪个扩展。
CREATE TABLE field_defs (
  id          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  object      VARCHAR(16)  NOT NULL,
  key_name    VARCHAR(96)  NOT NULL,
  label       VARCHAR(64)  NOT NULL,
  type        VARCHAR(16)  NOT NULL,
  options     TEXT         NOT NULL,
  access      TINYINT      NOT NULL DEFAULT 0,
  source      VARCHAR(96)  NOT NULL DEFAULT '',
  created_by  BIGINT       NULL,
  created_at  DATETIME(3)  NOT NULL,
  updated_at  DATETIME(3)  NOT NULL,
  UNIQUE KEY uk_field_defs_key (object, key_name)
);

-- 字段值。value 是 JSON（字符串、数字或字符串数组）。manual=1 表示人手工改过，同步不覆盖。
CREATE TABLE field_values (
  object      VARCHAR(16)  NOT NULL,
  object_id   BIGINT       NOT NULL,
  field_id    BIGINT       NOT NULL,
  value       TEXT         NOT NULL,
  manual      TINYINT      NOT NULL DEFAULT 1,
  updated_by  BIGINT       NULL,
  updated_at  DATETIME(3)  NOT NULL,
  PRIMARY KEY (object, object_id, field_id),
  KEY idx_field_values_field (field_id)
);

-- 访问规则：库级「谁能查」之上的附加条件，只会收紧。字段按键引用（配置导出导入时不依赖 id）。
CREATE TABLE access_rules (
  id          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name        VARCHAR(64)  NOT NULL,
  enabled     TINYINT      NOT NULL DEFAULT 0,
  op          VARCHAR(16)  NOT NULL,
  user_field  VARCHAR(96)  NOT NULL,
  doc_field   VARCHAR(96)  NOT NULL,
  missing     VARCHAR(16)  NOT NULL DEFAULT 'deny',
  kbases      TEXT         NOT NULL,
  created_by  BIGINT       NULL,
  created_at  DATETIME(3)  NOT NULL,
  updated_at  DATETIME(3)  NOT NULL,
  UNIQUE KEY uk_access_rules_name (name)
);

-- 管理后台多一页「字段与访问规则」。全新部署的菜单由启动时写入，下面这句不生效。
INSERT INTO menus (parent_id, kind, title, path, page_key, icon, permission, sort, visible, created_at, updated_at)
SELECT g.id, 'page', '字段与访问规则', '/admin/fields', 'admin-fields', 'Tags', 'fields.manage', 40, 1, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
FROM (SELECT id FROM menus WHERE kind = 'group' AND title = '账号与权限' AND parent_id IS NULL ORDER BY id DESC LIMIT 1) g
WHERE NOT EXISTS (SELECT 1 FROM menus WHERE page_key = 'admin-fields');
