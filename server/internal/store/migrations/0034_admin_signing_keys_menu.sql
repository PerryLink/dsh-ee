-- 扩展签名的信任根管理页（#134 的后端接口 + 本 PR 的网页）：给已有部署补一条菜单。
-- 全新部署的菜单表是空的，由启动时写入默认菜单（rbac.DefaultMenus），下面的语句不生效。
-- 页面挂在「企业连接」旁边：装扩展的管理员顺手管受信任公钥；管理员挪过企业连接就跟着它。
INSERT INTO menus (parent_id, kind, title, path, page_key, icon, permission, sort, visible, created_at, updated_at)
SELECT c.parent_id, 'page', '扩展签名', '/admin/signing-keys', 'admin-signing-keys', 'ShieldCheck', 'connections.manage', c.sort + 10, 1, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
FROM menus c
WHERE c.page_key = 'admin-connections'
  AND NOT EXISTS (SELECT 1 FROM menus WHERE page_key = 'admin-signing-keys')
ORDER BY c.id LIMIT 1;
