// Textos tras conectar el origen npm integrado: comprobaciones automáticas, revisión, gestión de capacidades, tokens y tareas de precarga
import type zhNs from "../zh/registry";
import type { Messages } from "../types";

export default {
  "registry": {
    "settingsSaveFailed": "La configuración no se guardó: {{msg}}",
    "scan": {
      "none": "No hay resultados de comprobaciones automáticas.",
      "bundle": {
        "label": "Bundle de DSH",
        "pass": "Declara dsh.bundle; el manifiesto del bundle {{path}} está en el paquete",
        "missing": "No declara dsh.bundle: es un paquete npm normal y no aparecerá en el mercado de capacidades ni en el índice de DSH",
        "no-patch": "Declara dsh.bundle, pero falta el campo patch",
        "unsafe": "dsh.bundle.patch no es una ruta relativa segura: {{path}}",
        "file-missing": "La ruta {{path}} a la que apunta dsh.bundle.patch no está en el paquete (¿falta la compilación o una entrada en files?)"
      },
      "patch": {
        "label": "Contenido del manifiesto del bundle",
        "pass": "{{path}} tiene una estructura válida ({{rows}} filas)",
        "warn": "{{path}}: {{reason}}",
        "fail": "{{path}}: {{reason}}",
        "reason": {
          "yaml": "No es YAML válido ({{error}})",
          "not-array": "El nivel superior debe ser un array",
          "empty": "No hay ninguna fila de patch",
          "row-not-object": "La fila {{row}} no es un objeto",
          "insert-not-array": "El insert de la fila {{row}} debe ser un array",
          "missing-id": "A la fila {{row}} le falta el row id",
          "duplicate-id": "El row id {{id}} está duplicado (filas {{row}} y {{first}})",
          "missing-name": "Un elemento insertado de la fila {{row}} no tiene name",
          "name-mismatch": "Ninguna fila hace referencia a este paquete ({{name}}); DSH no encontrará el plugin",
          "config-not-map": "El config de {{name}} no es un mapping",
          "unreadable": "El archivo patch es demasiado grande o no se pudo leer"
        }
      },
      "skills": {
        "label": "Habilidades incluidas en el paquete",
        "pass": "El paquete no contiene SKILL.md",
        "warn": "Incluye {{count}} habilidades: {{files}}; funcionan con el plugin, sin listado, permisos ni retirada independientes."
      },
      "cordis": {
        "label": "Dependencias de Cordis",
        "warn": "Mueve {{packages}} a peerDependencies, o devDependencies si solo se usa al compilar; duplicar Cordis rompe la inyección y el ciclo de vida."
      },
      "cordis-peer": {
        "label": "Versión de Cordis",
        "warn": "El rango {{range}} de Cordis en peerDependencies no incluye {{want}}, que usa el DSH 0.2.2 actual; si también debe ser compatible con hosts antiguos, revise el rango."
      },
      "install-scripts": {
        "label": "Scripts de instalación",
        "pass": "No se ejecutan scripts durante la instalación",
        "warn": "Durante la instalación se ejecutarán {{scripts}}: confirme que no descargan ni ejecutan código externo al instalar"
      },
      "secrets": {
        "label": "Fuga de secretos",
        "pass": "No se han encontrado secretos ni archivos sensibles sospechosos",
        "fail": "Se han encontrado {{count}} posibles secretos o archivos sensibles: {{files}}"
      },
      "license": {
        "label": "License",
        "pass": "{{license}}",
        "warn": "No declara license"
      },
      "engines": {
        "label": "Versión de DSH",
        "pass": "engines.dsh: {{range}}",
        "warn": "No declara engines.dsh; DSH determinará la compatibilidad según las dependencias peer"
      },
      "dependencies": {
        "label": "Dependencias de la comunidad",
        "pass": "{{count}}: {{names}}"
      },
      "size": {
        "label": "Tamaño",
        "pass": "{{size}} (descomprimido {{unpacked}}, {{files}} archivos)",
        "warn": "{{size}} (descomprimido {{unpacked}}, {{files}} archivos), demasiado grande"
      },
      "manifest-mismatch": {
        "label": "Coherencia del manifiesto",
        "fail": "El package.json del tarball indica {{inner}}, que no coincide con los metadatos"
      }
    },
    "review": {
      "requested": "Solicitud de un empleado",
      "publishedBy": "Publicado por {{name}} · {{department}}",
      "requestedBy": "Solicitado por {{name}}",
      "previous": "Versión publicada actual: v{{version}}",
      "firstVersion": "Primera publicación",
      "dependencies": "dependencies",
      "peers": "peerDependencies",
      "engines": "engines",
      "scripts": "scripts",
      "downloadTarball": "Descargar el tarball para revisarlo"
    },
    "plugins": {
      "empty": "Aún no hay capacidades. Aparecerán aquí cuando un empleado publique una, se precargue desde el origen o se importe sin conexión.",
      "versions": "Versiones",
      "pendingVersions": "{{count}} versiones pendientes de revisión",
      "yankAll": "Despublicar todas las versiones de {{name}}; los empleados ya no podrán instalarla:",
      "yankVersion": "Despublicar {{name}}:",
      "reasonPlaceholder": "Motivo de la despublicación (obligatorio; se registra en la versión)",
      "publisher": "Publicador / solicitante",
      "created": "Fecha de envío",
      "note": "Registro de revisión",
      "reviewedBy": "Revisado por {{name}}"
    },
    "detail": {
      "deprecated": "Obsoleto: {{msg}}",
      "deprecatedShort": "Obsoleto",
      "npmInstall": "Instalar con pnpm / npm en el proyecto",
      "engines": "Requiere DSH {{range}}; versión recomendada para la empresa: {{version}}.",
      "readme": "Descripción",
      "versions": "Versiones publicadas",
      "size": "Tamaño del tarball",
      "homepage": "Página principal"
    },
    "setup": {
      "tokenPlaceholder": "<su token>",
      "tokenCount": "{{count}}",
      "prefix": "Prefijo",
      "never": "Nunca",
      "tokenNamePlaceholder": "p. ej. «Mi portátil»",
      "tokenOnce": "El token completo se muestra solo esta vez; el comando de abajo ya está rellenado automáticamente:",
      "revokeConfirm": "Al revocar «{{name}}», el equipo que lo usa no podrá instalar desde el origen de la empresa de inmediato. ¿Continuar?",
      "scriptNpmrc": "Escribe el registry, el scope de la empresa y su token en el .npmrc del directorio de perfil de DSH (sin tocar ~/.npmrc; defina antes DSH_EE_GLOBAL_NPMRC=1 para que sea global)",
      "scriptIndex": "Le indica que añada el origen del índice en DSH → página de plugins → Discover:",
      "caDesc": "Pida el certificado CA al administrador, guárdelo como ~/.dsh-ee-ca.pem y ejecute:"
    },
    "publish": {
      "cliDesc": "Genere primero un token «publicable» en la <link>guía de integración</link> y escríbalo en ~/.npmrc; después, en el directorio del plugin, ejecute:",
      "fileHint": "El .tgz generado por npm pack; el nombre del paquete, la versión, el nombre visible y la categoría se leen de package.json",
      "tag": "dist-tag",
      "tagHint": "Predeterminada: latest; para versiones preliminares puede usar beta",
      "uploading": "Subiendo…",
      "publishedNow": "{{name}} se ha publicado y ya está disponible",
      "resultPending": "Se ha enviado {{name}}; los colegas podrán instalarlo cuando el administrador lo apruebe.",
      "resultPublished": "{{name}} está publicado.",
      "note": "Comentario de revisión: {{note}}",
      "manifestTitle": "Defina todo en package.json",
      "manifestDesc": "Declara el plugin con dsh.bundle; usa campos dsh-ee para su presentación.",
      "exampleDesc": "Una frase que explique qué hace",
      "exampleTitle": "Mi plugin"
    },
    "admin": {
      "checkOk": "Conexión con el origen (upstream) {{upstream}} correcta ({{ms}} ms)",
      "checkFail": "Error al conectar con el origen: {{msg}}",
      "upstreamPlugins": "De ellos, {{count}} plugins; el resto son dependencias",
      "versions": "{{count}} versiones",
      "downloads30d": "Descargas de los últimos 30 días",
      "pending": "{{count}} versiones pendientes de revisión",
      "indexUrl": "Origen del índice de plugins de DSH",
      "tokensCount": "{{count}} tokens válidos",
      "noTokens": "Aún nadie ha creado un token.",
      "revokeConfirm": "¿Revocar el token «{{name}}» de {{user}}? El equipo que lo usa no podrá instalar de inmediato.",
      "tokensHint": "El personal crea tokens en la <link>guía de conexión</link>; usa una cuenta dedicada para CI."
    },
    "upstream": {
      "publishDirectly": "Publicar directamente después de sincronizar",
      "publishDirectlyHint": "Omite la revisión: el plugin aparecerá de inmediato en el mercado de capacidades y en el índice de DSH.",
      "checkUpdates": "Buscar nuevas versiones ahora",
      "noUpdates": "Todos los plugins precargados están en la última versión",
      "closureNote": "La precarga replica las dependencias necesarias para instalar sin conexión.",
      "feedCount": "Catálogo de plugins: {{count}} plugins · actualizado el {{at}}",
      "refreshFeed": "Actualizar catálogo",
      "feedDisabled": "El catálogo está desactivado; importa sin conexión o solicita un paquete.",
      "feedError": "No se pudo obtener el catálogo dshfind: {{msg}}",
      "newer": "Origen {{version}}",
      "importDesc": "Importa un paquete sin conexión; se rechazan resúmenes distintos y los plugins requieren revisión.",
      "importing": "Importando…",
      "importResult": "{{added}} versiones nuevas ({{pending}} pendientes de revisión), {{existing}} ya existentes, {{skipped}} paquetes omitidos.",
      "exportAllHint": "Déjelo vacío para exportar todos los paquetes de origen publicados"
    },
    "jobs": {
      "kind": {
        "preload": "Precarga de origen",
        "request": "Solicitud de empleado",
        "update": "Sincronización de nueva versión",
        "import": "Importación sin conexión"
      },
      "status": {
        "running": "En curso",
        "succeeded": "Completado",
        "partial": "Parcialmente fallido",
        "failed": "Fallido",
        "interrupted": "Interrumpido"
      },
      "colKind": "Tarea",
      "colCount": "Éxito / total",
      "colStarted": "Hora de inicio",
      "viewLog": "Ver registro"
    }
  }
} satisfies Messages<typeof zhNs>;
