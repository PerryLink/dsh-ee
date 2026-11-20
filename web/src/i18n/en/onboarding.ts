import type zh from "../zh/onboarding";
import type { Messages } from "../types";

export default {
  onboarding: {
    listSep: ", ",
    steps: {
      base_url: {
        title: "Access URL",
        hint: "The address employee computers use to reach dsh-ee; their DSH and npm connect back through it.",
        detail: {
          from_settings: "{{url}} (from system settings)",
          from_flag: "{{url}} (from the DSH_EE_PUBLIC_URL startup setting)",
        },
      },
      provider: {
        title: "Model provider",
        hint: "Connect a model provider. You need an upstream API key.",
        hints: {
          unhealthy: "Re-check the provider; if its API key is wrong, fix it under Model access.",
        },
        detail: {
          enabled: "{{enabled}} enabled provider(s)",
          unhealthy: "{{enabled}} enabled provider(s), {{unhealthy}} failing recent checks",
        },
      },
      models: {
        title: "Public models",
        hint: "List the models employees can use under a provider.",
      },
      default_model: {
        title: "Default model",
        hint: "Pick one of the listed models as the employees' default.",
        hints: {
          not_listed: "Change the default to one of the listed models.",
        },
        detail: {
          not_listed: "Default model {{model}} is not among the listed models",
        },
      },
      quota: {
        title: "Employee quota",
        hint: "Give employees a monthly quota. New accounts start at 0 and can't call models.",
        detail: {
          has_quota: "{{ok}} account(s) have a quota",
          has_quota_zero: "{{ok}} account(s) have a quota, {{zero}} still at 0 (new accounts can't call models)",
          none_assigned: "{{humans}} employee account(s), none with a quota yet",
        },
      },
      ldap: {
        title: "LDAP / AD sign-in",
        hint: "Optional. Connect your LDAP / AD so employees sign in with company accounts.",
      },
      roles: {
        title: "Departments and roles",
        hint: "Optional. Create departments and roles to grant access by department.",
        detail: {
          summary: "{{roles}} custom role(s), {{depts}} department(s)",
        },
      },
      connections: {
        title: "Company connections",
        hint: "Optional. Connect company systems (needs upstream credentials) so employees can use them in DSH.",
        detail: {
          count: "{{count}} connection(s)",
        },
      },
      demo: {
        title: "Demo company",
        detail: {
          imported: "Demo scenario imported (demo company or small team): knowledge base, apps, experts, demo accounts and 30 days of usage data. You can clear it in one click before going live.",
        },
      },
      connect: {
        title: "DSH connect plugin",
        hint: "Publish the DSH connect plugin for employees. Republish after changing the access URL or default model.",
        hints: {
          disabled: "Turn DSH connect back on in settings, then publish.",
        },
        detail: {
          disabled: "DSH connect is turned off",
          unpublished: "Not published yet",
        },
      },
    },
    title: "First-time setup guide",
    desc: "Complete the required configuration in order. Finished steps are skipped automatically.",
    kicker: "WELCOME TO DSH-EE",
    heroTitle: "Get your company AI workspace ready",
    heroDesc: "Follow the guided steps to finish your first deployment. Each step has a clear next action, and you will enter the workbench automatically when done.",
    progress: "Setup progress {{done}}/{{total}}",
    requiredLabel: "Required steps",
    dialogDesc: "Configure this step here and the setup progress will refresh automatically.",
    baseUrl: "Employee access URL",
    baseUrlHint: "A complete http(s) URL reachable from employee computers",
    providerName: "Provider name",
    apiKey: "Company API key",
    modelNames: "Public models",
    modelNamesHint: "Separate multiple models with commas",
    provider: "Model provider",
    defaultModel: "Default model",
    defaultModelAuto: "Use the first model automatically",
    employee: "Employee account",
    employeeHint: "Super admins do not count toward employee quota. Create a regular employee",
    newEmployee: "Create employee account",
    username: "Username",
    password: "Initial password",
    department: "Department",
    monthlyQuota: "Monthly token quota (10k)",
    monthlyQuotaHint: "For example, 10 means 100,000 tokens per month",
    quotaInvalid: "Enter a quota greater than zero",
    allowedModels: "Allowed models (all when empty)",
    allowedModelsHint: "If no model is selected, all models are allowed",
    saveContinue: "Save and continue",
    optional: "Optional",
    done: "Done",
    next: "Next",
    handle: "Configure",
    publish: "Publish connect plugin",
    publishing: "Publishing…",
    agentTitle: "Let the agent in DSH finish the setup",
    agentDesc: "Once the steps that need secrets are done here, paste a prompt into DSH. It installs the connect plugin, asks you to sign in and turn on admin mode, then walks you through quota, LDAP, roles and connections, asking you to confirm every change. The checklist below refreshes automatically.",
    agentNeed: "Finish these below first: {{steps}} (they need upstream secrets, so they can only be done here)",
    agentStart: "Create prompt",
    published: "Connect plugin {{version}} published",
    inviteTitle: "Invite employees to connect",
    inviteDesc: "After the required steps, share this command with employees. They can also install Desktop from the download page.",
  },
} satisfies Messages<typeof zh>;
