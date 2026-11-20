// साइन-इन और खाता।
import type zhNs from "../zh/auth";
import type { Messages } from "../types";

export default {
  "login": {
    "title": "वर्कबेंच में साइन इन करें",
    "subtitle": "व्यवस्थापक द्वारा आपके लिए बनाए गए खाते का उपयोग करें।",
    "username": "उपयोगकर्ता नाम",
    "password": "पासवर्ड",
    "submit": "साइन इन करें",
    "submitting": "साइन इन हो रहा है…",
    "forgot": "पासवर्ड भूल गए? कृपया व्यवस्थापक से रीसेट करवाएँ।",
    "ldapHint": "आप सीधे कंपनी के डोमेन खाते (कंप्यूटर के लॉगिन पासवर्ड जैसा) से साइन इन कर सकते हैं।",
    "ldapForgot": "डोमेन खाते का पासवर्ड कंपनी के खाता सिस्टम से पुनः प्राप्त करें; स्थानीय खाते का पासवर्ड भूल गए तो व्यवस्थापक से संपर्क करें।",
    "reauth": "आप अभी DSH से स्वतः साइन इन हैं और आपके पास केवल कर्मचारी अनुमतियाँ हैं। सभी सुविधाओं के लिए पासवर्ड से साइन इन करें।",
    "handoffExpired": "DSH से खोला गया साइन-इन लिंक समाप्त हो गया है (केवल एक बार, 1 मिनट तक वैध), कृपया साइन इन करके जारी रखें।",
    "handoffOtherSession": "This browser is already signed in with another account, so the sign-in link from DSH won't replace the existing session. Sign out of the current account first, then open the link from DSH again.",
    "demo": {
      "title": "यह एक सार्वजनिक डेमो साइट है",
      "account": "उपयोगकर्ता नाम {{username}} · पासवर्ड {{password}}",
      "reset": "डेटा {{reset}} रीसेट होता है, बेझिझक आज़माएँ।",
      "fill": "डेमो खाता भरें",
      "heroTitle": "Xinghe Tech के कर्मचारी ऐप आज़माएँ",
      "heroSubtitle": "CRM, खर्च, टिकट और ज्ञान आज़माने के लिए कर्मचारी चुनें।",
      "uses": "सामान्य: {{app}}",
      "enterAs": "{{name}} के रूप में प्रवेश करें",
      "scenarios": {
        "pick": "Pick a demo scenario",
        "xinghe": {
          "tab": "Company · Xinghe Tech"
        },
        "novel": {
          "tab": "Small team · Shanhai Studio",
          "heroTitle": "Shanhai Studio के लेखन ऐप आज़माएँ",
          "heroSubtitle": "संदर्भ, अध्याय सहयोग और विशेषज्ञ समीक्षा आज़माने के लिए सदस्य चुनें।"
        }
      },
      "adminDivider": "या सेटअप और समीक्षा देखने के लिए एडमिन के रूप में लॉगिन करें।",
      "dsh": {
        "title": "DSH को डेमो से जोड़ें",
        "intro": "अपने स्थानीय मॉडल से डेमो ऐप और ज्ञान आज़माएँ।",
        "step1": "टर्मिनल में चलाएँ:",
        "unix": "macOS / Linux",
        "win": "Windows",
        "step2": "ब्राउज़र में पुष्टि पेज खुलने पर इस पेज पर कोई कर्मचारी पहचान चुनकर साइन इन करें, फिर “पुष्टि करें” पर क्लिक करें।",
        "step3": "DSH में लौटकर /customer-brief जैसा ऐप नाम टाइप करें और ऊपर के कार्ड में दिया प्रश्न पूछें।",
        "note": "अपना मॉडल इस्तेमाल करें; डेमो डेटा रीसेट होने पर फिर जुड़ें।"
      }
    }
  },
  "initAdmin": {
    "title": "एडमिन खाता बनाएं",
    "subtitle": "यह नया dsh-ee है, इसमें अभी कोई खाता नहीं है। पहला एडमिनिस्ट्रेटर बनाएं।",
    "code": "सेटअप कोड",
    "codeHint": "सर्वर लॉग में मिलेगा: docker compose logs dsh-ee | grep init_code",
    "username": "एडमिन यूज़रनेम",
    "password": "पासवर्ड",
    "passwordHint": "कम से कम 10 अक्षर",
    "confirm": "पासवर्ड दोबारा लिखें",
    "mismatch": "दोनों पासवर्ड मेल नहीं खाते",
    "submit": "बनाएं और साइन इन करें",
    "submitting": "बना रहे हैं…"
  },
  "account": {
    "shortcuts": {
      "title": "DSH कनेक्शन और डेवलपमेंट",
      "hint": "ये पेज साइडबार में नहीं हैं; इन्हें यहाँ से खोलें।",
      "connect": "कनेक्ट",
      "develop": "डेवलप",
      "getStarted": "शुरू करें",
    },
    "title": "खाता",
    "logout": "साइन आउट करें",
    "username": "उपयोगकर्ता नाम",
    "displayName": "प्रदर्शित नाम",
    "department": "विभाग",
    "roles": "भूमिकाएँ",
    "preferences": "Preferences",
    "preferencesHint": "Language and theme are saved on this device only.",
    "theme": "Theme",
    "changePassword": "पासवर्ड बदलें",
    "currentPassword": "वर्तमान पासवर्ड",
    "newPassword": "नया पासवर्ड",
    "newPasswordHint": "कम से कम 10 अक्षर। बदलने के बाद सभी डिवाइस पर दोबारा साइन इन करना होगा।",
    "confirmPassword": "नया पासवर्ड दोबारा दर्ज करें",
    "source": "खाता स्रोत",
    "sourceLocal": "स्थानीय खाता",
    "sourceLdap": "एंटरप्राइज़ निर्देशिका (LDAP)",
    "email": "ईमेल",
    "ldapManaged": "आपका खाता एंटरप्राइज़ निर्देशिका द्वारा प्रबंधित है, पासवर्ड कंपनी के खाता सिस्टम में बदलें; प्रदर्शित नाम और विभाग हर साइन-इन पर निर्देशिका से सिंक होते हैं।",
    "mismatch": "दोनों बार दर्ज किए गए नए पासवर्ड मेल नहीं खाते",
    "language": "Language",
    "languageHint": "Used for the web interface and messages such as Feishu notifications. Changes apply immediately and sync to your account.",
    "followOrg": "Follow organization default"
  }
} satisfies Messages<typeof zhNs>;
