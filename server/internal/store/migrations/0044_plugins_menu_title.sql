-- 叫法统一：管理后台的「能力管理」页面标题本来就是「插件管理」，菜单跟着改。只改还是旧默认名的项。
UPDATE menus SET title = '插件管理', updated_at = UTC_TIMESTAMP(3)
WHERE kind = 'page' AND page_key = 'admin-plugins' AND title = '能力管理';
