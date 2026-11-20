export default {
  onboarding: {
    listSep: "、",
    steps: {
      base_url: {
        title: "访问地址",
        hint: "填写员工电脑可访问的地址",
        detail: {
          from_settings: "{{url}}（来自系统设置）",
          from_flag: "{{url}}（来自启动参数 DSH_EE_PUBLIC_URL）",
        },
      },
      provider: {
        title: "模型供应商",
        hint: "接入一个模型供应商，需要上游 API key",
        hints: {
          unhealthy: "重新检测供应商；API key 有问题要在「模型接入」里改",
        },
        detail: {
          enabled: "{{enabled}} 个启用的供应商",
          unhealthy: "{{enabled}} 个启用的供应商，其中 {{unhealthy}} 个最近检测不通",
        },
      },
      models: {
        title: "上架对外模型",
        hint: "在供应商下上架员工可以用的模型",
      },
      default_model: {
        title: "默认模型",
        hint: "选一个已上架的模型作为员工的默认模型",
        hints: {
          not_listed: "把默认模型改成已上架的模型",
        },
        detail: {
          not_listed: "默认模型 {{model}} 不在已上架的模型里",
        },
      },
      quota: {
        title: "员工额度",
        hint: "给员工分配每月额度（新账号默认 0，不能调用模型）",
        detail: {
          has_quota: "{{ok}} 个账号有额度",
          has_quota_zero: "{{ok}} 个账号有额度，{{zero}} 个还是 0（新账号默认不能调用模型）",
          none_assigned: "{{humans}} 个员工账号，都还没分配额度",
        },
      },
      ldap: {
        title: "LDAP / AD 登录",
        hint: "可选。接入公司的 LDAP / AD，员工用公司账号登录",
      },
      roles: {
        title: "部门与角色",
        hint: "可选。建部门和角色，按部门分配权限",
        detail: {
          summary: "{{roles}} 个自定义角色，{{depts}} 个部门",
        },
      },
      connections: {
        title: "企业连接",
        hint: "可选，使用上游凭据接入企业系统",
        detail: {
          count: "{{count}} 个",
        },
      },
      demo: {
        title: "示例公司",
        detail: {
          imported: "已导入示例场景；正式使用前可清除",
        },
      },
      connect: {
        title: "DSH 接入插件",
        hint: "发布接入插件；地址或默认模型变更后须重新发布",
        hints: {
          disabled: "先在设置里重新打开 DSH 接入，再发布",
        },
        detail: {
          disabled: "DSH 接入已被关闭",
          unpublished: "还没有发布",
        },
      },
    },
    title: "首次部署向导",
    desc: "按清单完成必做配置；已经完成的步骤会自动跳过。",
    kicker: "WELCOME TO DSH-EE",
    heroTitle: "完成首次部署",
    heroDesc: "按清单完成首次部署。",
    progress: "部署进度 {{done}}/{{total}}",
    requiredLabel: "必做步骤",
    dialogDesc: "在当前页完成配置，保存后自动刷新进度。",
    baseUrl: "员工访问地址",
    baseUrlHint: "员工电脑可访问的完整 http(s) 地址",
    providerName: "供应商名称",
    apiKey: "企业 API key",
    modelNames: "对外模型",
    modelNamesHint: "多个模型用英文逗号分隔",
    provider: "模型供应商",
    defaultModel: "默认模型",
    defaultModelAuto: "自动选择第一个模型",
    employee: "员工账号",
    employeeHint: "超级管理员不计入员工额度，请新建普通员工",
    newEmployee: "新建员工账号",
    username: "用户名",
    password: "初始密码",
    department: "部门",
    monthlyQuota: "每月 token 额度（万）",
    monthlyQuotaHint: "例如 10 表示每月 10 万 token",
    quotaInvalid: "请输入大于 0 的额度",
    allowedModels: "可用模型（不选表示全部）",
    allowedModelsHint: "不勾选任何模型时允许全部模型",
    saveContinue: "保存并继续",
    optional: "可选",
    done: "已完成",
    next: "下一步",
    handle: "去处理",
    publish: "发布接入插件",
    publishing: "发布中…",
    agentTitle: "交给 DSH 里的 Agent 配完",
    agentDesc: "先在网页填密钥，再登录 DSH、开启管理模式并发送提示词；每次改动须确认。",
    agentNeed: "先在网页填写密钥：{{steps}}",
    agentStart: "生成提示词",
    published: "接入插件 {{version}} 已发布",
    inviteTitle: "邀请员工接入",
    inviteDesc: "完成必做项后，把接入命令发给员工。",
  },
};
