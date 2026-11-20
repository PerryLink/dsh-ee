/** Guía de configuración inicial (primer despliegue). */
import type zhNs from "../zh/onboarding";
import type { Messages } from "../types";

export default {
  "onboarding": {
    "listSep": ", ",
    "steps": {
      "base_url": {
        "title": "URL de acceso",
        "hint": "Indica una dirección accesible al personal.",
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
        "hint": "Opcional: conecta sistemas con credenciales del servicio.",
        "detail": {
          "count": "{{count}} conexión(es)",
        },
      },
      "demo": {
        "title": "Empresa de demostración",
        "detail": {
          "imported": "Ejemplos importados; puedes eliminarlos antes del uso real.",
        },
      },
      "connect": {
        "title": "Plugin de conexión DSH",
        "hint": "Vuelve a publicar el plugin si cambias la dirección o el modelo predeterminado.",
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
    "heroTitle": "Configura tu instalación",
    "heroDesc": "Completa la instalación siguiendo la lista.",
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
    "agentDesc": "Introduce las claves aquí y envía el mensaje a DSH. Inicia sesión y activa el modo administrador; debes confirmar cada cambio del agente.",
    "agentNeed": "Introduce primero las claves aquí: {{steps}}",
    "agentStart": "Crear prompt",
    "published": "Plugin de conexión {{version}} publicado",
    "inviteTitle": "Invita a los empleados a conectarse",
    "inviteDesc": "Completa los pasos obligatorios y comparte el comando de conexión."
  }
} satisfies Messages<typeof zhNs>;
