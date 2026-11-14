-- 企业插件目录管理页（docs/plugin-signing.md §8）：给已有部署补一条菜单。
-- 全新部署的菜单表是空的，由启动时写入默认菜单（rbac.DefaultMenus），下面的语句不生效。
-- 页面挂在「扩展签名」后面：装扩展 / 管插件的管理员顺手看目录；管理员挪过企业连接就跟着它。
INSERT INTO menus (parent_id, kind, title, path, page_key, icon, permission, sort, visible, created_at, updated_at)
SELECT c.parent_id, 'page', '企业插件目录', '/admin/directory', 'admin-directory', 'FolderOpen', 'connections.manage', c.sort + 10, 1, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
FROM menus c
WHERE c.page_key = 'admin-connections'
  AND NOT EXISTS (SELECT 1 FROM menus WHERE page_key = 'admin-directory')
ORDER BY c.id LIMIT 1;
