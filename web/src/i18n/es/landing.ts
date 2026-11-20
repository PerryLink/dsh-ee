// Página de aterrizaje
import type zhNs from "../zh/landing";
import type { Messages } from "../types";

export default {
  "audit": {
    "run": "Ejecutar",
    "install": "Instalar",
    "publish": "Enviar",
    "approve": "Aprobar",
    "yank": "Despublicar"
  },
  "landing": {
    "sources": {
      "skill": {
        "ask": "¿Se puede reembolsar esta factura de hotel de Shanghái de 1.280 CNY?",
        "answer": "Sí. El límite de alojamiento en Shanghái es de 1.300 CNY por noche y hay que adjuntar también el extracto del hotel."
      },
      "system": {
        "ask": "¿Qué clientes de la región Este vencen el mes que viene?",
        "answer": "Son 14; 3 de ellos no tienen visitas registradas en los últimos 90 días. Ya están ordenados por importe."
      },
      "data": {
        "ask": "¿Cuál es la diferencia entre GMV y volumen de negocio?",
        "answer": "El GMV incluye pedidos no pagados y cancelados; el volumen de negocio solo cuenta los pagados. Responsable de la definición: xu.ran."
      },
      "context": {
        "ask": "¿Qué permisos hay que activar la primera semana de un nuevo empleado?",
        "answer": "Son 6: GitLab, Jira, VPN, base de datos de pruebas en solo lectura, grupo de Feishu y calendario de guardias; consulte la sección 2 del manual."
      }
    },
    "hub": {
      "systems": "Sistemas actuales de la empresa",
      "sys": {
        "erp": "ERP",
        "crm": "CRM",
        "oa": "OA",
        "ticket": "Tickets",
        "dw": "Almacén de datos",
        "bi": "Informes BI"
      },
      "assets": "Activos de la empresa",
      "asset": {
        "skill": "Skill",
        "kb": "Base de conocimiento",
        "prompt": "Prompt",
        "plugin": "Plugin",
        "policy": "Política"
      },
      "connector": "MCP / OpenAPI",
      "dataset": "Conjunto de datos de la plataforma",
      "hubChip": "Centro de capacidades",
      "center": "Despliegue privado de la empresa",
      "assemble": "Ensamblar como microaplicación",
      "grant": "Revisión · Autorización",
      "apps": {
        "expense": "Asistente de gastos",
        "renewal": "Seguimiento de renovaciones",
        "metrics": "Consultas de gestión",
        "onboard": "Guía de incorporación"
      },
      "appFrom": {
        "expense": "Política + Skill",
        "renewal": "Conexión CRM",
        "metrics": "Conjunto de datos de la plataforma",
        "onboard": "Base de conocimiento del manual de I+D"
      },
      "dept": {
        "expense": "Finanzas",
        "renewal": "Ventas",
        "metrics": "Gestión",
        "onboard": "I+D"
      },
      "desc": {
        "expense": "La política de viajes se convierte en un Skill y los límites de gasto se responden al instante",
        "renewal": "CRM conectado mediante MCP; consulta de clientes con renovación según permisos",
        "metrics": "Los criterios de las métricas van al conjunto de datos de la plataforma; todos usan la misma cifra",
        "onboard": "El manual de I+D va a la base de conocimiento y las respuestas incluyen fuentes"
      },
      "pick": "Elija un caso de habilitación",
      "window": "DSH · equipo del empleado",
      "calling": "Llamando a {{pkg}}",
      "called": "{{pkg}} llamado"
    },
    "fragment": {
      "nav": {
        "overview": "Resumen",
        "apps": "Microaplicaciones",
        "market": "Mercado de capacidades",
        "setup": "Guía de integración"
      },
      "apps": "Microaplicaciones",
      "runs": "{{department}} · {{runs}} ejecuciones en los últimos 14 días",
      "openInDsh": "Abrir en DSH",
      "auditLog": "Registro de auditoría",
      "today": "Hoy",
      "os": "Sistema operativo del dispositivo",
      "devices": "{{n}} dispositivos"
    },
    "why": {
      "eyebrow": "Por qué dsh-ee",
      "title": "De las pruebas individuales a un uso controlado para toda la empresa",
      "desc": "Da al personal acceso seguro al conocimiento y los sistemas de la empresa.",
      "before": {
        "tag": "Cada empleado usa herramientas de IA por su cuenta",
        "title": "Individual, puntual, sin registro",
        "knowledge": "Las políticas y los criterios de métricas los copian y pegan los empleados en el chat",
        "trust": "Plugins y Skills de origen desconocido; nadie los revisa antes de instalarlos",
        "trace": "Qué sistema llamó quién y desde qué equipo no se puede rastrear después",
        "reuse": "Los prompts y flujos útiles se quedan en el equipo de cada persona"
      },
      "after": {
        "tag": "Distribuido de forma centralizada por dsh-ee",
        "title": "Activos de la empresa, capacidad predeterminada para todos",
        "knowledge": "Políticas, sistemas y datos empaquetados como capacidades; se instalan y se preguntan directamente",
        "trust": "Cada paquete pasa primero las comprobaciones automáticas y después la aprobación del administrador y la autorización por departamento",
        "trace": "Los registros de instalación y llamadas vuelven a auditoría; si hay un problema, se puede despublicar con un clic",
        "reuse": "Los nuevos Skills que crean los empleados vuelven al ámbito de la empresa para que todos los reutilicen"
      }
    },
    "controls": {
      "xinchuang": {
        "title": "Tecnología nacional en toda la cadena",
        "desc": "Ejecuta servidores, clientes, bases de datos y modelos en infraestructura nacional."
      },
      "control": {
        "title": "Controlable",
        "desc": "Despliegue privado o sin conexión con revisión y permisos; los paquetes internos no recurren al origen."
      },
      "audit": {
        "title": "Auditable",
        "desc": "Rastrea instalaciones y llamadas, y retira capacidades cuando haga falta."
      }
    },
    "steps": {
      "deploy": {
        "title": "Despliegue",
        "desc": "Despliega con Docker o un paquete sin conexión y OceanBase o TiDB."
      },
      "capture": {
        "title": "Consolidación",
        "desc": "Publica habilidades, conectores y conocimiento en el ámbito de empresa."
      },
      "review": {
        "title": "Revisión y publicación",
        "desc": "Tras las comprobaciones automáticas, los administradores revisan y autorizan."
      },
      "use": {
        "title": "Uso por los empleados",
        "desc": "Conecta con un comando y usa las aplicaciones de empresa en DSH."
      }
    },
    "features": {
      "open": "Expandir: {{title}}",
      "close": "Cerrar",
      "what": "Qué hace",
      "how": "Cómo se usa",
      "hub": {
        "title": "Centro de capacidades: plugins, habilidades y conexiones en un solo lugar",
        "lead": "Gestiona capacidades de empresa y sincroniza las habilitadas con DSH.",
        "points": [
          "Cinco tipos de capacidades en pestañas, cada una con versión, alcance de disponibilidad y registro de llamadas",
          "Defina la visibilidad por departamento o rol y configúrela como instalación automática",
          "Desactive o despublica en cualquier momento; el lado del empleado se sincroniza"
        ],
        "steps": [
          "Abra «Centro de capacidades» y elija o busque la capacidad que necesita",
          "Defina el alcance: toda la empresa, un departamento concreto o un rol concreto",
          "Pulse «Activar» y los empleados podrán usarla la próxima vez que abran DSH"
        ],
        "cta": "Ir al Centro de capacidades",
        "ui": {
          "tabs": [
            "Plugins",
            "Habilidades",
            "Prompts",
            "Conexiones de empresa",
            "Microaplicaciones"
          ],
          "scopes": [
            "Toda la empresa",
            "Departamento de Finanzas",
            "Departamento de Ventas",
            "Centro de I+D"
          ],
          "synced": "Sincronizado en 1.497 DSH"
        }
      },
      "review": {
        "title": "Primero revisar, luego publicar",
        "lead": "Comprueba los plugins automáticamente y revísalos antes de publicarlos.",
        "points": [
          "Comprobación automática de scripts de instalación, dependencias, licencia y fugas de secretos",
          "Las habilidades, los prompts y las microaplicaciones se comparan con la versión en línea para ver los cambios",
          "Al rechazar se indica el motivo y quien lo envió puede verlo en «Mis envíos»"
        ],
        "steps": [
          "El empleado sube el .tgz en «Compartir capacidad» o ejecuta npm publish directamente",
          "El revisor consulta los resultados y los cambios en «Pendiente de revisión»",
          "Tras la aprobación entra en el ámbito de la empresa y se distribuye según el alcance de disponibilidad"
        ],
        "cta": "Ver la cola de revisión",
        "ui": {
          "checks": [
            "Sin scripts de instalación",
            "Dependencias replicadas",
            "Licencia conforme",
            "Escaneo de secretos superado"
          ],
          "reject": "Rechazar",
          "approve": "Aprobar"
        }
      },
      "connect": {
        "title": "Integración con un solo comando",
        "lead": "Ejecuta el comando de conexión y confirma el código del dispositivo.",
        "points": [
          "macOS / Linux usan setup.sh; Windows usa setup.ps1",
          "El token unificado se cruza con los permisos y scopes de la cuenta; el servidor solo guarda el hash",
          "Gestión en cualquier momento desde el chat de DSH con /ee-login, /ee-status y /ee-logout"
        ],
        "steps": [
          "Copie el comando de la «Guía de integración» y ejecútelo en la terminal",
          "Abra en el navegador la dirección que se indica y confirme el código de dispositivo",
          "Vuelva a DSH: las capacidades y los modelos de la empresa ya están disponibles"
        ],
        "cta": "Ver la guía de integración",
        "ui": {
          "confirm": "Confirme el código de dispositivo en el navegador",
          "done": "Dispositivo confirmado; DSH conectado al origen de la empresa"
        }
      },
      "knowledge": {
        "title": "Preguntas y respuestas sobre la base de conocimiento, con fuentes en cada frase",
        "lead": "Sube documentos, pregunta en DSH y consulta las fuentes.",
        "points": [
          "Admite PDF, Word y Markdown; los documentos tienen versiones y se pueden revertir",
          "Defina por base quién puede consultar y quién la mantiene",
          "Los enlaces a las fuentes llevan al original y resaltan las líneas correspondientes"
        ],
        "steps": [
          "Cree una base de conocimiento, defina la visibilidad y suba documentos",
          "Haga una búsqueda de prueba para confirmar que se encuentra",
          "Los empleados abren «Preguntas y respuestas» en DSH y preguntan directamente"
        ],
        "cta": "Ir a las bases de conocimiento",
        "ui": {
          "ask": "En un viaje de trabajo, ¿cuál es el límite por noche de hotel?",
          "answer": "En ciudades de primer nivel, hasta 1.300 CNY por noche; en otras ciudades, hasta 800 CNY. Si se supera, se requiere la aprobación del responsable del departamento.",
          "source": "Política de viajes y gastos v3 · líneas 12–18",
          "searched": "3 rondas de búsqueda, 2 textos originales leídos"
        }
      },
      "systems": {
        "title": "Sistemas de la empresa, autorizados por herramienta",
        "lead": "Conecta sistemas mediante MCP u OpenAPI y autoriza cada herramienta.",
        "points": [
          "Compatible con servicios MCP e interfaces OpenAPI",
          "Credenciales custodiadas de forma centralizada, sin distribuirlas a los equipos de los empleados",
          "Cada herramienta se autoriza por separado y todas las llamadas quedan registradas"
        ],
        "steps": [
          "Añada un servicio MCP o una dirección OpenAPI en «Conexiones de empresa»",
          "Introduzca las credenciales y marque las herramientas y los alcances de autorización que se abrirán",
          "Los empleados piden al agente que las use directamente desde DSH"
        ],
        "cta": "Ver conexiones de empresa",
        "ui": {
          "conn": "CRM (servicio MCP)",
          "vault": "Credenciales custodiadas",
          "tools": [
            "Consultar cliente",
            "Consultar historial de renovaciones",
            "Modificar importe del contrato"
          ],
          "scopes": [
            "Departamento de Ventas",
            "Departamento de Ventas · Atención al cliente",
            "Sin autorizar"
          ]
        }
      },
      "models": {
        "title": "Puerta de enlace de modelos y cuotas",
        "lead": "Conecta modelos con conmutación por fallo y cuotas por empleado.",
        "points": [
          "Comprobación de conexión con el origen, importación de modelos y nombres externos",
          "Un mismo modelo con varios orígenes y conmutación automática ante errores",
          "Cuota mensual, modelos disponibles y vigencia por cuenta; al superar la cuota se devuelve 429"
        ],
        "steps": [
          "Añada orígenes e importe modelos en «Conexión de modelos»",
          "Asigne una cuota mensual a las cuentas en «Asignación de cuotas»",
          "Consulte el uso por departamento y modelo en «Datos operativos»"
        ],
        "cta": "Ver conexión de modelos",
        "ui": {
          "usage": "Tokens usados en los últimos 30 días",
          "quota": "Cuota mensual del Centro de I+D",
          "upstreams": [
            "DeepSeek (oficial)",
            "Puerta de enlace de modelos interna"
          ],
          "status": [
            "Principal",
            "Reserva"
          ]
        }
      }
    },
    "nav": {
      "how": "Cómo funciona",
      "why": "Por qué",
      "workbench": "Banco de trabajo",
      "control": "Control y auditoría",
      "deploy": "Despliegue"
    },
    "enter": "Entrar al banco de trabajo",
    "heroLine1": "Que el DSH de cada empleado conozca su empresa.",
    "heroLead": "Conecta sistemas y conocimiento de empresa para usarlos con autorización en DSH.",
    "tryDemo": "Probar como empleado",
    "videoLabel": "Vídeo de presentación de dsh-ee (1 min)",
    "seeHow": "Ver cómo funciona",
    "howTitle": "Cómo funciona",
    "howLine": "De los activos de la empresa a las manos de los empleados, cada paso es controlable",
    "wbLine1": "Los activos se convierten en microaplicaciones",
    "wbLine2": "y se ejecutan en DSH",
    "wbDesc": "Aloja aplicaciones en dsh-ee y ejecútalas en el DSH local del personal.",
    "toDsh": "dsh-ee aporta a DSH",
    "toDshDesc": "Skills y contexto de empresa, un origen de instalación de confianza, microaplicaciones y políticas de uso.",
    "toEe": "DSH aporta a dsh-ee",
    "toEeDesc": "El motor de ejecución, los registros de instalación y llamadas, y los nuevos Skills que crean los empleados.",
    "controlLine1": "Controlable y auditable,",
    "controlLine2": "ejecutándose en entornos nacionales",
    "deployLine1": "Despliegue en diez minutos,",
    "deployLine2": "integración con un solo comando",
    "codeServer": "# Servidor",
    "codeClient": "# Equipo del empleado; una sola integración",
    "codeUse": "# A partir de aquí, use DSH con normalidad",
    "authNote": "Acceso local, LDAP u OIDC; verifica instaladores antes de importarlos.",
    "closing": "Haga que el conocimiento de la empresa sea la capacidad predeterminada del DSH de cada empleado.",
    "footerNote": "Prototipo; todos los datos son simulados. Los datos de plugins de origen proceden de dshfind.com.",
    "footerMarket": "Mercado de capacidades",
    "footerAdmin": "Consola de administración"
  }
} satisfies Messages<typeof zhNs>;
