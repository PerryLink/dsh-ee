/** «Empezar»: pega un mensaje en DSH y conecta automáticamente el banco de trabajo de la empresa. */
import type zhNs from "../zh/get-started";
import type { Messages } from "../types";

export default {
  "getStarted": {
    "title": "Empezar con {{org}}",
    "subtitle": "Envía el mensaje a DSH para instalar el plugin e iniciar sesión.",
    "later": "Ahora no",
    "enter": "Ir al banco de trabajo",
    "other": "¿Prefieres la terminal?",
    "otherLink": "Usar el comando de una línea",
    "retry": "Reintentar",
    "step1": "① Envía esto en DSH",
    "pathPrompt": "DSH ya puede chatear",
    "pathManual": "DSH recién instalado",
    "promptDesc": "Envíalo en un chat nuevo de DSH y sigue las instrucciones de acceso.",
    "manualAdd": "En DSH, abre Plugins → Añadir plugin y pega esta dirección:",
    "manualRestart": "Reinicia DSH cuando termine la instalación.",
    "manualLogin": "Escribe esto en el cuadro de chat:",
    "noDsh": "¿Aún no tienes DSH?",
    "download": "Descargar la aplicación de escritorio",
    "step2": "② Confirma el inicio de sesión aquí",
    "waiting": "Esperando a que DSH inicie el inicio de sesión… (código de configuración {{code}}, válido hasta {{time}})",
    "expired": "El código de configuración ha caducado.",
    "regenerate": "Crear uno nuevo",
    "pending": "«{{client}}» ({{os}}, {{ip}}) quiere iniciar sesión como tú. Código de confirmación:",
    "pendingHint": "Asegúrate de que coincide con el código que muestra DSH antes de confirmar. Deniega si no has iniciado esto.",
    "approve": "Autorizar",
    "deny": "Denegar",
    "approved": "Autorizado. Esperando a que DSH termine de iniciar sesión…",
    "denied": "Denegado. Para volver a iniciar sesión, introduce {{cmd}} en DSH.",
    "connected": "«{{client}}» está conectado",
    "step3Admin": "③ Activar el modo de administración",
    "adminDesc": "Introduce tu contraseña para dar acceso temporal de administrador a este DSH. El agente no puede activarlo.",
    "password": "Contraseña",
    "hours": "{{n}} h",
    "adminOnButton": "Activar el modo de administración",
    "adminOn": "El modo de administración está activado",
    "step3": "③ Continuar en DSH",
    "step4Admin": "④ Continuar en DSH",
    "nextDesc": "Envía este mensaje para ver y habilitar modelos y habilidades disponibles.",
    "nextDescAdmin": "Envía este mensaje para continuar con la configuración."
  }
} satisfies Messages<typeof zhNs>;
