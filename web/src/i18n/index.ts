/**
 * 界面多语言。中文是源语言（i18n/zh），英文（i18n/en）按中文的结构写，缺键会编译报错。
 * 新增文案：先在 zh 对应模块加键，再在 en 同一模块补上；组件里用 useTranslation() 的 t("模块.键")。
 * 后端返回的错误信息与模拟数据暂未翻译。
 */
import i18n from "i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { initReactI18next } from "react-i18next";
import zh from "./zh";
import en from "./en";
import pt from "./pt";

export const LANGUAGES = [
  { code: "zh-CN", label: "简体中文" },
  { code: "en", label: "English" },
  { code: "pt", label: "Português" },
] as const;

export type Lang = (typeof LANGUAGES)[number]["code"];

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "translation";
    resources: { translation: typeof zh };
  }
}

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: { "zh-CN": { translation: zh }, en: { translation: en }, pt: { translation: pt } },
    supportedLngs: LANGUAGES.map((l) => l.code),
    // 其它语言的浏览器看英文
    fallbackLng: "en",
    detection: {
      order: ["localStorage", "navigator"],
      lookupLocalStorage: "dsh-ee:lang",
      caches: ["localStorage"],
      // zh-TW、zh-HK 等都用简体中文，en-US、en-GB 用英文。
      // 注意不要改用 nonExplicitSupportedLngs：在当前 i18next 版本下它会让 zh-CN 取不到文案。
      convertDetectedLanguage: (lng: string) =>
      lng.startsWith("zh") ? "zh-CN" : lng.startsWith("en") ? "en" : lng.startsWith("pt") ? "pt" : lng,
    },
    interpolation: { escapeValue: false },
    returnNull: false,
  });

const syncHtmlLang = (lng: string) => {
  document.documentElement.lang = lng.startsWith("zh") ? "zh-CN" : lng;
};
syncHtmlLang(i18n.resolvedLanguage ?? "en");
i18n.on("languageChanged", syncHtmlLang);

/** 当前界面语言是否是中文（用于挑选数据里自带的多语言字段）。 */
export const isZh = () => (i18n.resolvedLanguage ?? "zh-CN").startsWith("zh");

export default i18n;
