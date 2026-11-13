-- 「开发」分组：开发文档（/docs）与开发样例（/examples）的侧栏入口。
-- 页面本身是前端固定路由（web/src/main.tsx），菜单只负责入口、标题与可见权限（portal.view）。
-- 全新部署的菜单表是空的，由启动时写入默认菜单（rbac.DefaultMenus），下面的语句都不生效。
-- 幂等：分组或页面已存在时不再插入；管理员改过标题的项不覆盖。

INSERT INTO menus (parent_id, kind, title, path, page_key, icon, permission, sort, visible, created_at, updated_at)
SELECT NULL, 'group', '开发', NULL, NULL, NULL, NULL, 19, 1, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
FROM DUAL WHERE EXISTS (SELECT 1 FROM menus)
  AND NOT EXISTS (SELECT 1 FROM menus WHERE kind = 'group' AND parent_id IS NULL AND title = '开发');

INSERT INTO menus (parent_id, kind, title, path, page_key, icon, permission, sort, visible, created_at, updated_at)
SELECT g.id, 'page', '开发文档', '/docs', 'dev-docs', 'BookMarked', 'portal.view', 10, 1, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
FROM menus g
WHERE g.kind = 'group' AND g.parent_id IS NULL AND g.title = '开发'
  AND NOT EXISTS (SELECT 1 FROM menus WHERE page_key = 'dev-docs')
ORDER BY g.id DESC LIMIT 1;

INSERT INTO menus (parent_id, kind, title, path, page_key, icon, permission, sort, visible, created_at, updated_at)
SELECT g.id, 'page', '开发样例', '/examples', 'dev-examples', 'FlaskConical', 'portal.view', 20, 1, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
FROM menus g
WHERE g.kind = 'group' AND g.parent_id IS NULL AND g.title = '开发'
  AND NOT EXISTS (SELECT 1 FROM menus WHERE page_key = 'dev-examples')
ORDER BY g.id DESC LIMIT 1;
