/**
 * 全局搜索（⌘K）里的设置项：每项指向一个页面里的一节（元素 id）。
 * 页面本身由菜单自动列出，不用登记；这里只登记「页面里的某一项设置」，以及搜索时用到的别名。
 * 新增设置分节时：给那一节加 id，在这里加一行，在 i18n 的 searchItems 里写标题。
 */
import type { PageKey } from "./page-meta";

export interface SearchItem {
  /** 也是目标元素的 id 和 searchItems 的文案键。 */
  id: string;
  /** 所在页面；菜单里没有这个页面（没权限）时不显示。account 是固定路由。 */
  page: PageKey | "account";
  keywords: string[];
}

export const SEARCH_ITEMS: SearchItem[] = [
  { id: "settings-basics", page: "admin-settings", keywords: ["组织", "公司名", "访问地址", "域名", "作用域", "scope", "base url", "organization"] },
  { id: "settings-scoring", page: "admin-settings", keywords: ["评分", "打分", "dshfind", "预载", "score", "rating"] },
  { id: "settings-admission", page: "admin-settings", keywords: ["收录", "审核", "白名单", "开放", "allowlist", "admission"] },
  { id: "settings-upstream", page: "admin-settings", keywords: ["上游", "镜像", "npmmirror", "离线", "同步", "upstream", "offline", "mirror"] },
  { id: "settings-distribution", page: "admin-settings", keywords: ["下载", "分发", "desktop", "download"] },
  { id: "settings-connect", page: "admin-settings", keywords: ["接入插件", "默认模型", "发布", "轮询", "设备令牌", "agent", "connect", "default model", "dsh-connect"] },
  { id: "settings-write-confirm", page: "admin-settings", keywords: ["写操作", "确认", "自动同意", "Claude Code", "Codex", "write", "confirm", "approval"] },
  { id: "settings-demo", page: "admin-settings", keywords: ["示例", "演示", "星河科技", "导入", "demo", "sample"] },
  { id: "brand-basics", page: "admin-brand", keywords: ["品牌", "名称", "名字", "侧栏", "标题", "首屏", "brand", "name", "title"] },
  { id: "brand-logo", page: "admin-brand", keywords: ["logo", "图标", "标志", "上传", "圆形", "icon"] },
  { id: "brand-theme", page: "admin-brand", keywords: ["配色", "颜色", "主题", "主色", "侧边栏", "皮肤", "theme", "color", "colour"] },
  { id: "brand-preview", page: "admin-brand", keywords: ["预览", "preview"] },
  { id: "auth-ldap", page: "admin-auth", keywords: ["ldap", "ad", "域账号", "目录", "同步", "登录", "active directory"] },
  { id: "auth-oidc", page: "admin-auth", keywords: ["oidc", "sso", "单点登录", "统一登录", "keycloak", "authing", "azure", "飞书", "钉钉"] },
  { id: "account", page: "account", keywords: ["密码", "账号", "个人", "password", "profile"] },
];
