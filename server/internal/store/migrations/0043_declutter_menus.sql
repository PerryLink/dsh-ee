-- 员工侧栏减负：默认只放每天用的页面（企业应用、知识问答、我的工作台、新建应用、我的应用）。
-- 样板、原料、接入三页（下载 DSH / 接入指南 / 我的令牌）、开发两页保留路由，只是不进侧栏：
-- 「我的应用」页有样板和原料入口，没接入 DSH 时侧栏顶部有「开始使用」，账号页列出接入与开发入口。
-- 只改还是默认路由、仍然显示的项；管理员改过路由或已经隐藏的不动。全新部署由 rbac.DefaultMenus 写入。
UPDATE menus SET visible = 0, updated_at = UTC_TIMESTAMP(3)
WHERE kind = 'page' AND visible = 1 AND (
     (page_key = 'templates' AND path = '/build/templates')
  OR (page_key = 'materials' AND path = '/build/materials')
  OR (page_key = 'dsh-download' AND path = '/dsh')
  OR (page_key = 'setup' AND path = '/setup')
  OR (page_key = 'my-keys' AND path = '/tokens')
  OR (page_key = 'dev-docs' AND path = '/docs')
  OR (page_key = 'dev-examples' AND path = '/examples')
);
