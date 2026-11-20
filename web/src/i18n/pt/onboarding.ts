// Assistente de primeira implantação.
import type zhNs from "../zh/onboarding";
import type { Messages } from "../types";

export default {
  onboarding: {
    listSep: ", ",
    steps: {
      base_url: {
        title: "URL de acesso",
        hint: "Informe um endereço acessível aos funcionários.",
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
        hint: "Opcional: conecte sistemas com credenciais do serviço.",
        detail: {
          count: "{{count}} conexão(ões)",
        },
      },
      demo: {
        title: "Empresa de demonstração",
        detail: {
          imported: "Exemplos importados; remova antes do uso real, se necessário.",
        },
      },
      connect: {
        title: "Plugin de conexão DSH",
        hint: "Republique o plugin ao mudar o endereço ou modelo padrão.",
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
    heroTitle: "Configure sua implantação",
    heroDesc: "Conclua a implantação seguindo a lista.",
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
    agentDesc: "Informe as chaves aqui e envie o prompt ao DSH. Entre e ative o modo administrador; cada alteração do agente exige sua confirmação.",
    agentNeed: "Informe primeiro as chaves aqui: {{steps}}",
    agentStart: "Gerar prompt",
    published: "Plugin de conexão {{version}} publicado",
    inviteTitle: "Convidar funcionários para conectar",
    inviteDesc: "Conclua os passos obrigatórios e compartilhe o comando de conexão.",
  },
} satisfies Messages<typeof zhNs>;
