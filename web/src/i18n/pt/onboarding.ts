// Assistente de primeira implantação.
import type zhNs from "../zh/onboarding";
import type { Messages } from "../types";

export default {
  onboarding: {
    listSep: ", ",
    steps: {
      base_url: {
        title: "URL de acesso",
        hint: "O endereço que os computadores dos funcionários usam para chegar ao dsh-ee; o DSH e o npm deles se conectam por ele.",
        detail: {
          from_settings: "{{url}} (das configurações do sistema)",
          from_flag: "{{url}} (do parâmetro de inicialização DSH_EE_PUBLIC_URL)",
        },
      },
      provider: {
        title: "Fornecedor de modelos",
        hint: "Conecte um fornecedor de modelos. É preciso uma chave de API do fornecedor.",
        hints: {
          unhealthy: "Verifique o fornecedor de novo; se a chave de API estiver errada, corrija em Acesso a modelos.",
        },
        detail: {
          enabled: "{{enabled}} fornecedor(es) ativo(s)",
          unhealthy: "{{enabled}} fornecedor(es) ativo(s), {{unhealthy}} falhando nas últimas verificações",
        },
      },
      models: {
        title: "Modelos públicos",
        hint: "Publique os modelos que os funcionários podem usar em um fornecedor.",
      },
      default_model: {
        title: "Modelo padrão",
        hint: "Escolha um dos modelos publicados como padrão dos funcionários.",
        hints: {
          not_listed: "Troque o padrão por um dos modelos publicados.",
        },
        detail: {
          not_listed: "O modelo padrão {{model}} não está entre os modelos publicados",
        },
      },
      quota: {
        title: "Cota dos funcionários",
        hint: "Dê aos funcionários uma cota mensal. Contas novas começam com 0 e não podem chamar modelos.",
        detail: {
          has_quota: "{{ok}} conta(s) com cota",
          has_quota_zero: "{{ok}} conta(s) com cota, {{zero}} ainda com 0 (contas novas não podem chamar modelos)",
          none_assigned: "{{humans}} conta(s) de funcionário, nenhuma com cota ainda",
        },
      },
      ldap: {
        title: "Login LDAP / AD",
        hint: "Opcional. Conecte seu LDAP / AD para os funcionários entrarem com a conta da empresa.",
      },
      roles: {
        title: "Departamentos e papéis",
        hint: "Opcional. Crie departamentos e papéis para dar acesso por departamento.",
        detail: {
          summary: "{{roles}} papel(éis) personalizado(s), {{depts}} departamento(s)",
        },
      },
      connections: {
        title: "Conexões da empresa",
        hint: "Opcional. Conecte sistemas da empresa (exige credenciais) para usá-los no DSH.",
        detail: {
          count: "{{count}} conexão(ões)",
        },
      },
      demo: {
        title: "Empresa de demonstração",
        detail: {
          imported: "Cenário de demonstração importado (empresa de demonstração ou equipe pequena): base de conhecimento, apps, especialistas, contas de demonstração e 30 dias de dados de uso. Dá para apagar com um clique antes do uso real.",
        },
      },
      connect: {
        title: "Plugin de conexão DSH",
        hint: "Publique o plugin de conexão DSH para os funcionários. Publique de novo depois de mudar a URL de acesso ou o modelo padrão.",
        hints: {
          disabled: "Reative a conexão DSH nas configurações e depois publique.",
        },
        detail: {
          disabled: "A conexão DSH está desativada",
          unpublished: "Ainda não publicado",
        },
      },
    },
    title: "Assistente de primeira implantação",
    desc: "Conclua as configurações obrigatórias seguindo o checklist; etapas já concluídas são ignoradas automaticamente.",
    kicker: "BEM-VINDO AO DSH-EE",
    heroTitle: "Configure as capacidades da empresa de uma vez",
    heroDesc: "Siga passos claros para concluir a primeira implantação. Cada passo tem um destino definido e, ao concluir, você entra automaticamente na área de trabalho.",
    progress: "Progresso da implantação {{done}}/{{total}}",
    requiredLabel: "Etapas obrigatórias",
    dialogDesc: "Conclua a configuração nesta página; após salvar, o progresso é atualizado automaticamente.",
    baseUrl: "Endereço de acesso dos funcionários",
    baseUrlHint: "Endereço http(s) completo acessível nos computadores dos funcionários",
    providerName: "Nome do fornecedor",
    apiKey: "Chave de API da empresa",
    modelNames: "Modelos expostos",
    modelNamesHint: "Separe vários modelos por vírgulas",
    provider: "Fornecedor de modelos",
    defaultModel: "Modelo padrão",
    defaultModelAuto: "Selecionar automaticamente o primeiro modelo",
    employee: "Conta de funcionário",
    employeeHint: "Superadministradores não entram na cota de funcionários; crie um funcionário comum",
    newEmployee: "Criar conta de funcionário",
    username: "Nome de usuário",
    password: "Senha inicial",
    department: "Departamento",
    monthlyQuota: "Cota mensal de tokens (10 mil)",
    monthlyQuotaHint: "Por exemplo, 10 significa 100 mil tokens por mês",
    quotaInvalid: "Insira uma cota maior que 0",
    allowedModels: "Modelos disponíveis (nenhum selecionado = todos)",
    allowedModelsHint: "Se nenhum modelo for marcado, todos serão permitidos",
    saveContinue: "Salvar e continuar",
    optional: "Opcional",
    done: "Concluído",
    next: "Próximo",
    handle: "Resolver",
    publish: "Publicar plugin de conexão",
    publishing: "Publicando…",
    agentTitle: "Deixe o Agent do DSH concluir a configuração",
    agentDesc: "Depois de concluir na web as etapas que exigem chaves, gere um prompt e cole no DSH: ele instala o plugin de conexão, pede seu login e ativa o modo de administração, e então segue o checklist para configurar cotas, LDAP, perfis e conexões, com sua confirmação a cada alteração. O checklist abaixo é atualizado automaticamente.",
    agentNeed: "Conclua antes abaixo: {{steps}} (as chaves de upstream só podem ser preenchidas na web)",
    agentStart: "Gerar prompt",
    published: "Plugin de conexão {{version}} publicado",
    inviteTitle: "Convidar funcionários para conectar",
    inviteDesc: "Depois de concluir as etapas obrigatórias, envie a linha de comando abaixo aos funcionários; você também pode instalar o Desktop pela página de download.",
  },
} satisfies Messages<typeof zhNs>;
