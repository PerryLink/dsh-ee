/** Guía de configuración inicial (primer despliegue). */
import type zhNs from "../zh/onboarding";
import type { Messages } from "../types";

export default {
  "onboarding": {
    "listSep": ", ",
    "steps": {
      "base_url": {
        "title": "URL de acceso",
        "hint": "La dirección con la que los equipos de los empleados llegan a dsh-ee; su DSH y npm se conectan a través de ella.",
        "detail": {
          "from_settings": "{{url}} (de la configuración del sistema)",
          "from_flag": "{{url}} (del parámetro de arranque DSH_EE_PUBLIC_URL)",
        },
      },
      "provider": {
        "title": "Proveedor de modelos",
        "hint": "Conecta un proveedor de modelos. Necesitas una clave de API del proveedor.",
        "hints": {
          "unhealthy": "Vuelve a comprobar el proveedor; si la clave de API es incorrecta, corrígela en Acceso a modelos.",
        },
        "detail": {
          "enabled": "{{enabled}} proveedor(es) activo(s)",
          "unhealthy": "{{enabled}} proveedor(es) activo(s), {{unhealthy}} con fallos en las últimas comprobaciones",
        },
      },
      "models": {
        "title": "Modelos públicos",
        "hint": "Publica los modelos que los empleados pueden usar dentro de un proveedor.",
      },
      "default_model": {
        "title": "Modelo predeterminado",
        "hint": "Elige uno de los modelos publicados como predeterminado para los empleados.",
        "hints": {
          "not_listed": "Cambia el predeterminado a uno de los modelos publicados.",
        },
        "detail": {
          "not_listed": "El modelo predeterminado {{model}} no está entre los modelos publicados",
        },
      },
      "quota": {
        "title": "Cuota de empleados",
        "hint": "Asigna a los empleados una cuota mensual. Las cuentas nuevas empiezan en 0 y no pueden llamar a modelos.",
        "detail": {
          "has_quota": "{{ok}} cuenta(s) con cuota",
          "has_quota_zero": "{{ok}} cuenta(s) con cuota, {{zero}} todavía en 0 (las cuentas nuevas no pueden llamar a modelos)",
          "none_assigned": "{{humans}} cuenta(s) de empleado, ninguna con cuota todavía",
        },
      },
      "ldap": {
        "title": "Inicio de sesión LDAP / AD",
        "hint": "Opcional. Conecta tu LDAP / AD para que los empleados entren con su cuenta de empresa.",
      },
      "roles": {
        "title": "Departamentos y roles",
        "hint": "Opcional. Crea departamentos y roles para dar acceso por departamento.",
        "detail": {
          "summary": "{{roles}} rol(es) personalizado(s), {{depts}} departamento(s)",
        },
      },
      "connections": {
        "title": "Conexiones de empresa",
        "hint": "Opcional. Conecta sistemas de la empresa (requiere credenciales) para usarlos desde DSH.",
        "detail": {
          "count": "{{count}} conexión(es)",
        },
      },
      "demo": {
        "title": "Empresa de demostración",
        "detail": {
          "imported": "Escenario de demostración importado (empresa de demostración o equipo pequeño): base de conocimiento, apps, expertos, cuentas de demostración y 30 días de datos de uso. Puedes borrarlo con un clic antes del uso real.",
        },
      },
      "connect": {
        "title": "Plugin de conexión DSH",
        "hint": "Publica el plugin de conexión DSH para los empleados. Vuelve a publicarlo tras cambiar la URL de acceso o el modelo predeterminado.",
        "hints": {
          "disabled": "Vuelve a activar la conexión DSH en la configuración y luego publica.",
        },
        "detail": {
          "disabled": "La conexión DSH está desactivada",
          "unpublished": "Aún no publicado",
        },
      },
    },
    "title": "Guía de configuración inicial",
    "desc": "Completa la configuración obligatoria en orden. Los pasos terminados se omiten automáticamente.",
    "kicker": "BIENVENIDO A DSH-EE",
    "heroTitle": "Prepara el espacio de trabajo de IA de tu empresa",
    "heroDesc": "Sigue los pasos guiados para completar tu primer despliegue. Cada paso tiene una acción siguiente clara y, al terminar, entrarás automáticamente en el banco de trabajo.",
    "progress": "Progreso de la configuración {{done}}/{{total}}",
    "requiredLabel": "Pasos obligatorios",
    "dialogDesc": "Configura este paso aquí y el progreso de la configuración se actualizará automáticamente.",
    "baseUrl": "URL de acceso para empleados",
    "baseUrlHint": "Una URL http(s) completa accesible desde los equipos de los empleados",
    "providerName": "Nombre del proveedor",
    "apiKey": "Clave de API de la empresa",
    "modelNames": "Modelos públicos",
    "modelNamesHint": "Separa varios modelos con comas",
    "provider": "Proveedor de modelos",
    "defaultModel": "Modelo predeterminado",
    "defaultModelAuto": "Usar el primer modelo automáticamente",
    "employee": "Cuenta de empleado",
    "employeeHint": "Los superadministradores no cuentan para la cuota de empleados. Crea un empleado normal",
    "newEmployee": "Crear cuenta de empleado",
    "username": "Nombre de usuario",
    "password": "Contraseña inicial",
    "department": "Departamento",
    "monthlyQuota": "Cuota mensual de tokens (10 000)",
    "monthlyQuotaHint": "Por ejemplo, 10 significa 100 000 tokens al mes",
    "quotaInvalid": "Introduce una cuota mayor que cero",
    "allowedModels": "Modelos permitidos (todos si se deja vacío)",
    "allowedModelsHint": "Si no se selecciona ningún modelo, se permiten todos",
    "saveContinue": "Guardar y continuar",
    "optional": "Opcional",
    "done": "Hecho",
    "next": "Siguiente",
    "handle": "Configurar",
    "publish": "Publicar plugin de conexión",
    "publishing": "Publicando…",
    "agentTitle": "Deja que el agente de DSH termine la configuración",
    "agentDesc": "Cuando los pasos que necesitan secretos estén hechos aquí, pega un prompt en DSH. Instalará el plugin de conexión, te pedirá iniciar sesión y activar el modo de administración, y luego te guiará por las cuotas, LDAP, roles y conexiones pidiéndote que confirmes cada cambio. La lista de comprobación de abajo se actualiza automáticamente.",
    "agentNeed": "Primero termina estos pasos: {{steps}} (necesitan secretos upstream, así que solo se pueden hacer aquí)",
    "agentStart": "Crear prompt",
    "published": "Plugin de conexión {{version}} publicado",
    "inviteTitle": "Invita a los empleados a conectarse",
    "inviteDesc": "Después de los pasos obligatorios, comparte este comando con los empleados. También pueden instalar Desktop desde la página de descargas."
  }
} satisfies Messages<typeof zhNs>;
