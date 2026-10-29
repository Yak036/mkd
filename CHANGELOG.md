# Changelog

Todas las modificaciones relevantes de MKD se documentan aquí.

## [Unreleased]

### Añadido

- **Corrección de Colisión de Pestañas Swarm con >9 Agentes y Navegación por Overflow (`ui`)**:
  - **Eliminación de colisiones de pestañas (`swarmstate.go` y `swarmrun.go`)**:
    - `agent()` ahora asigna índices secuenciales únicos (`len(s.order) + 1`) sin clampear a 9, eliminando colisiones silenciosas entre sub-agentes en enjambres de más de 9 miembros.
    - `switchViewTab` y `nextViewTab` actualizados para admitir y ciclar cualquier índice válido (0..N).
    - `agentByTab` ahora itera de forma determinista sobre el orden de registro (`s.order`), eliminando la aleatoriedad de los maps de Go.
  - **Auto-reparación en restauración de sesión (`restoreSwarmState`)**:
    - Detección de colisiones de índices al restaurar sesiones para auto-reparar estados antiguos donde múltiples agentes compartían el índice 9, reasignando índices únicos secuenciales.
  - **Barra de pestañas con visibilidad garantizada del agente activo y desbordamiento (`swarmview.go`)**:
    - Nueva función `renderTabBarWithActiveVisible` que utiliza una ventana deslizante centrada en la pestaña activa para asegurar que el agente seleccionado siempre sea visible, independientemente del ancho de la terminal o de cuántos agentes existan.
    - Indicadores visuales claros de desbordamiento izquierdo y derecho (`◀ +N` / `+M ▶`) que informan cuántas pestañas quedan ocultas en cada dirección.
    - Insignia de pestaña activa e indicador de atajos en el header del inspector de agentes (`[Pestaña X · Alt+←/→]`).
  - **Navegación por teclado enriquecida (`workspace.go`)**:
    - Nuevos atajos `Alt+Right` / `Ctrl+Tab` (siguiente pestaña) y `Alt+Left` / `Ctrl+Shift+Tab` (pestaña anterior) para ciclar cíclicamente entre todas las pestañas (0..N).
    - Teclas de flecha horizontal (`←` / `→`) habilitadas para cambiar de agente cuando se está inspeccionando un sub-agente y el área de texto está vacía.
  - **Suite de Pruebas Unitarias**:
    - `TestSwarmTabPersistenceMoreThan9AgentsRoundTrip` y `TestSwarmRestoreLegacyCollisionSelfHeal` en `swarm_persistence_test.go`.
    - `TestSwarmKeyboardNavigationBeyond9` y `TestSwarmTabBarOverflowActiveVisible` en `swarm_ui_test.go`.

- **Soporte nativo para proveedor Google Antigravity (`agy`)**:
  - Detección automática del binario `agy` (`~/.local/bin/agy`, `/usr/local/bin/agy`, `PATH`) y de la cuenta activa de Google desde `~/.gemini/google_accounts.json`.
  - Nuevo comando CLI `mkd auth antigravity` (alias `mkd auth agy`):
    - Conecta automáticamente la cuenta detectada (`ramcesvedes@gmail.com`).
    - Sincroniza dinámicamente los 13 modelos disponibles (`gemini-3.8-flash-high/medium/low`, `gemini-4-argon-high/medium/low`, `claude-sonnet-5-5-*`, `claude-opus-5-5-*`, `gpt-oss-120b-medium`).
    - Establece `antigravity` como proveedor activo si se desea.
  - Nuevo paquete `internal/antigravity` (100% Go estándar, filosofía Ponytail sin librerías externas):
    - Detección de binario y credenciales locales sin requerir ingresar API keys.
    - Directriz balanceada de inferencia en `FormatPrompt` para evitar que el subproceso `agy` intente ejecutar herramientas locales de su propio runtime o falle por permisos de MCPs headless, preservando la compatibilidad de orquestación de MCPs nativos de MKD.
    - Flag `--dangerously-skip-permissions` y fallback automático a `Result.Response` cuando no hay deltas de streaming intermedios.
    - Notificación explícita de error si el proceso termina sin generar texto en lugar de descartar el turno silenciosamente en la TUI.
    - Streaming incremental en tiempo real mediante subprocess IPC con `agy --output-format stream-json --disable-slash-commands`.
    - Parser NDJSON de eventos `step_update` con fallback seguro de catálogo.
  - Corrección en `needsOnboarding()` de la TUI (`internal/ui/workspace.go`):
    - Permite arranque directo sin forzar modal de bienvenida cuando el proveedor activo ya cuenta con autenticación local válida (`provider.Configured() == true`), aplicando para Antigravity, Codex y Claude.
  - Integración en TUI (`internal/ui/modals.go`):
    - Tecla `Ctrl+I` en el modal de proveedor para vincular inmediatamente la sesión local de Antigravity.
    - Soporte completo en `/connect`, selector de modelos (`Ctrl+K` / `/model`) y sub-agentes Swarm.

- **Migración de Servidores MCP desde OpenCode y Antigravity**:
  - Incorporados 6 servidores MCP estándar en `~/.config/mkd/config.json`:
    - `filesystem` (14 tools): acceso a proyectos (`/home/ramces/Documentos/Proyectos`), cerebro (`/home/ramces/cerebro`) y home.
    - `memoria` (9 tools): grafo de memoria y relaciones persistentes (`@modelcontextprotocol/server-memory`).
    - `playwright` (24 tools): automatización completa de navegador con perfiles locales.
    - `ui-design` (16 tools): servidor everything para prototipado e interfaces.
    - `zai-mcp` (1 tool): scripts y utilidades de proyectos Zai.
    - `web-search`: configurado (deshabilitado por ausencia de paquete en registry).
  - Un total de 64 herramientas MCP activas accesibles interactivamente mediante el modal `/mcp` (`Ctrl+O`).

- **Selector interactivo de herramientas (Tools) y corrección de edición de prompt en sub-agentes Swarm**:
  - **Selector interactivo de Tools (`agentFieldTools`)**:
    - Reemplazo completo del `textinput` manual por un selector interactivo tipo badges (`[x]`/`[ ]`) con las herramientas nativas y MCP (`read_file`, `list_dir`, `search_code`, `patch_file`, `write_file`, `run_command`, `mcp:*`).
    - Navegación fluida con flechas horizontales (`◀/▶` o `h/l`) sobre las herramientas disponibles.
    - Activación y desactivación instantánea con `Espacio`, `Enter` o `x`.
    - Atajo `a` para alternar todas las herramientas (activar todas / desactivar todas).
    - Persistencia automática en `AllowedTools` al guardar con `Ctrl+S`.
  - **Corrección crítica de edición y navegación en el Prompt (`agentFieldPrompt`)**:
    - Solucionado el bloqueo/congelamiento del modal al enfocar el campo de Prompt: las flechas `Up` y `Down` ahora se transmiten correctamente al componente `textarea` de Bubbles permitiendo navegar libremente entre líneas de texto sin que el modal se quede pegado.
    - Sincronización dinámica de `SetHeight(available)` en el viewport del textarea para garantizar que el scroll interno mantenga el cursor y las líneas activas siempre visibles.
    - Navegación bidireccional entre campos: `Tab`, `PgDn` o `Ctrl+↓` para avanzar al siguiente campo; `Shift+Tab`, `PgUp` o `Ctrl+↑` para retroceder al selector de herramientas; y flecha `Up` desde la primera línea para subir al campo anterior.

- **Selector interactivo de proveedor, autocompletado y copia de prompt en sub-agentes Swarm**:
  - En el editor de sub-agentes (`Ctrl+T` / `/agents`), el selector de `Proveedor` filtra de manera estricta y muestra **únicamente** proveedores con conexión exitosa (`Connected == true`) además de `(global)`.
  - En el campo `Modelo` (`agentFieldModel`):
    - Autocompletado inteligente con `Tab` basado en el catálogo del proveedor seleccionado.
    - Ciclado directo de modelos conocidos con `Alt+◀/▶` o `Ctrl+◀/▶` sin tener que escribirlos manualmente.
    - Muestra en tiempo real las sugerencias de modelos disponibles para el proveedor elegido.
    - Persistencia integral de `Provider` y `Model` en `config.SubAgentProfile` y visualización en el panel de detalle del roster.
  - **Copia rápida de prompt/descripción al portapapeles**:
    - En el Roster (`Ctrl+T`), presionar `Ctrl+Y`, `Ctrl+C`, `c` o `y` copia inmediatamente el `RolePrompt` (prompt específico o descripción) del sub-agente seleccionado al portapapeles del sistema, con notificación en pantalla.
    - En el Editor, presionar `Ctrl+Y` copia el contenido actual del prompt al portapapeles.

- **Diferenciación estricta entre `/connect` y `/model` con verificación de conexión**:
  - `/connect` (`modalProvider`): Ahora actúa como el comando y modal dedicado a configurar, conectar y probar conectividad con el proveedor de IA.
    - Pulsar Enter ejecuta un test de conexión real (`testConnectionCmd`) contra la API del proveedor usando `FetchAvailableModels`.
    - Muestra un spinner interactivo mientras prueba la conexión.
    - Si la conexión falla: muestra el error en rojo en el propio modal de `/connect`, marca el proveedor con `Connected = false` y permanece en el modal (NO abre `/model`).
    - Si la conexión tiene éxito: marca el proveedor con `Connected = true`, sincroniza automáticamente los modelos reales devueltos por la API en la configuración y en la caché, y muestra confirmación en verde.
    - Visualización del estado en tiempo real (`● Conectado exitosamente` / `○ No conectado`).
  - `/model` (`modalModels`): Ahora filtra de forma estricta y muestra únicamente proveedores y modelos con conexión exitosa previa (`Connected == true`).
    - Si ningún proveedor ha tenido conexión exitosa en `/connect`, el modal no muestra modelos ficticios o rotos; en su lugar muestra una pantalla informativa guiando al usuario a utilizar `/connect`.
    - La navegación entre proveedores (`Ctrl+←/→`) se limita exclusivamente a proveedores conectados exitosamente.
    - El comando `/model add <id>` rechaza la operación si el proveedor activo no tiene una conexión exitosa verificada.

- **Soporte completo para suscripción de Claude (Anthropic Pro / Team / Enterprise)**:
  - Nuevo comando CLI `mkd auth claude`:
    - `mkd auth claude`: Flujo OAuth 2.0 PKCE en navegador con callback loopback (puerto 54321) y fallback interactivo para ingresar el código o token.
    - `mkd auth claude --import`: Importación instantánea de credenciales de Claude Code desde `~/.claude/.credentials.json`, `~/.claude.json`, variables `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY` o `~/.mkd/claude_auth.json`.
    - `mkd auth claude --token "sk-ant-..."`: Soporte directo para tokens setup de Claude Code (`sk-ant-oat...`) o API Keys tradicionales (`sk-ant-api...`).
    - Actualizado `mkd auth status` con estado y vigencia de la suscripción de Claude.
  - Nuevo paquete `internal/claude` implementado 100% en Go estándar (sin dependencias externas):
    - Flujo OAuth 2.0 PKCE con Client ID oficial de Claude Code (`9d1c250a-e61b-44d9-88ed-5944d1962f5e`).
    - Renovación automática y transparente de tokens expirados en segundo plano (`RefreshAccessToken`).
    - Soporte completo para Messages API de Anthropic (`https://api.anthropic.com/v1/messages`) con streaming SSE y Tool Calling unificado.
    - Detección automática de proxy de red (SOCKS5 local `127.0.0.1:10808` y variables de entorno) con exclusión de loopback.
  - Integración en la TUI:
    - Proveedor `Claude (Anthropic)` seleccionable en `Ctrl+K` y paleta de comandos.
    - Atajos interactivos en el modal de proveedor: `Ctrl+O` para login en navegador y `Ctrl+I` para importar sesión local.
    - Sincronización automática de modelos (`claude-3-7-sonnet-20250219`, `claude-3-5-sonnet-20241022`, `claude-3-5-haiku-20241022`, etc.).

- **Soporte completo para suscripción de OpenAI Codex (ChatGPT Plus / Pro / Team / Enterprise)**:
  - Nuevo comando CLI `mkd auth`:
    - `mkd auth codex`: Flujo OAuth 2.0 PKCE en navegador con listener HTTP local (puerto 1455) y canje transparente de tokens contra `auth.openai.com`.
    - `mkd auth codex --import`: Importación instantánea de credenciales existentes de `~/.codex/auth.json` o `$CODEX_HOME/auth.json`.
    - `mkd auth status`: Visualización del estado de autenticación, cuenta vinculada y vigencia del token.
  - Nuevo paquete `internal/codex` implementado 100% con librería estándar de Go (sin dependencias adicionales):
    - Flujo OAuth 2.0 PKCE (`app_EMoamEEZ73f0CkXaXp7hrann`).
    - Decodificación segura de claims JWT para extracción de `account_id` (`chatgpt_account_id`) y expiración.
    - Renovación automática y transparente de tokens en segundo plano (`RefreshAccessToken`).
    - Adaptador de transporte sobre SSE para el endpoint de suscripción de Codex (`https://chatgpt.com/backend-api/codex/responses` y `/models`).
  - Integración en la TUI:
    - Proveedor `Codex (ChatGPT)` configurable desde `Ctrl+K`.
    - Atajos interactivos en el modal de proveedor: `Ctrl+O` para login en navegador y `Ctrl+I` para importar sesión local.
    - Acción directa en la paleta de comandos (`Ctrl+P` -> `Autenticación Codex (ChatGPT)`).
    - Sincronización automática de los modelos habilitados en el plan del usuario al autenticarse.

- **Preguntas interactivas del agente (`ask_user`)**:
  - Nueva herramienta `ask_user` (`internal/agent/question.go`, `internal/agent/tools.go`)
    con `question`, `header`, `options[{label, description}]` y `allow_custom`.
    `ParseQuestion` valida y normaliza (máx. 6 opciones, etiquetas en una línea) y
    devuelve `ArgumentError` para la autocorrección del modelo.
  - Modal `modalQuestion` (`internal/ui/questionmodal.go`): `↑/↓` o `j/k` navegan,
    `1-6` elige directo, `Tab` salta a la respuesta libre, `Enter` confirma y
    `Esc`/`Ctrl+C` cancela. La respuesta siempre está disponible aunque haya
    opciones, así que el usuario puede escribir la suya.
  - El bucle de agente pausa las llamadas pendientes, devuelve la respuesta como
    `role=tool` y continúa la misma ronda; la pregunta queda registrada en el chat
    como `↳ respuesta: …`.
  - El system prompt del agente explica cuándo preguntar y que no se usa para
    pedir permiso de escritura/ejecución.
  - Las preguntas del **Orquestador Swarm** se enrutan a la misma UI con
    `swarm.AskUser` + `askUserBridge` (`internal/ui/swarmask.go`); si la misión se
    cancela, el orquestador deja de esperar. Los sub-agentes no preguntan: deciden
    y lo anotan en su reporte.

### Corregido

- **Pérdida de contexto al compactar**: el resumen se enviaba dos veces (como
  `Summary` y como mensaje `<context_summary>` del historial). Ahora
  `Compact` devuelve solo los mensajes recientes y `Prepare` filtra resúmenes
  heredados de sesiones antiguas.
- **Auto-compactación sin umbral**: `startAutoCompact` no llamaba a
  `shouldAutoCompact()`, así que resumía la conversación tras cualquier turno.
- **Turnos entrelazados**: enviar un mensaje mientras el agente generaba, esperaba
  un permiso o una pregunta mezclaba la secuencia `assistant(tool_calls)` →
  `role=tool`. Ahora `busy()` bloquea el envío y `cancelTurn()` centraliza Esc,
  `/new`, carga de sesión y limpieza.
- **Historial inválido tras cancelar**: `ai.SanitizeMessages` elimina
  `role=tool` huérfanos y `tool_calls` sin respuesta antes de cada petición
  (OpenAI, OpenRouter, DeepInfra y Ollama rechazan esa secuencia). Si un
  assistant pierde todas sus llamadas, su texto se conserva.
- **Respuestas tardías**: un resultado o una pregunta resuelta después de cancelar
  el turno o cambiar de sesión se descarta en vez de contaminar la conversación.

## [0.7.0] - 2026-10-27

### Añadido

- **Comprobación y actualización de versión**:
  - `mkd upgrade` descarga la última release, verifica el SHA-256 contra
    `checksums.txt` y reemplaza el binario en ejecución (rename atómico en Unix;
    `.old` en Windows, borrado al siguiente arranque).
  - `mkd upgrade --check` solo informa sin descargar.
  - Aviso discreto en la status bar: `↑ v0.7.0 disponible · mkd upgrade`.
  - Comprobación en segundo plano con caché de 24 h en
    `~/.local/share/mkd/update_check.json`; nunca bloquea el arranque.
  - Se desactiva con `MKD_NO_UPDATE_CHECK=1` y automáticamente en CI.
  - Las builds de desarrollo (`-dirty` / `dev`) no muestran avisos.

- **Soporte Windows completo**:
  - `install.ps1` para `irm ... | iex`: descarga `mkd.exe`, verifica checksum SHA-256,
    instala en `%LOCALAPPDATA%\mkd` y añade la carpeta al `PATH` del usuario (sin admin).
  - `run_command` usa **PowerShell** (`-NoProfile -NonInteractive`) y cae a `cmd.exe`,
    en lugar de `/bin/sh` que no existe en Windows.
  - Portapapeles con `clip.exe` como backend nativo.
  - `~\\ruta` se expande al home del usuario en `skills_dir`/`rules_dir`.
  - CI ejecuta la suite también en `windows-latest`.

### Corregido

- **Fondo 100% opaco y sólido en terminales transparentes (como OpenCode).**
  - Implementada la función `paintOpaqueBackground` que envuelve y preserva el color de fondo `surface` (`#18181B`) a través de todos los resets ANSI (`\x1b[0m` y `\x1b[m`), evitando que las áreas vacías o los resets de color dejen ver el fondo transparente del emulador de terminal (VS Code, Alacritty, Kitty, etc.).
  - Asignado fondo `surface` explícito a los estilos `userBoxStyle`, `assistantBoxStyle`, `inputBoxStyle` y a los componentes interactivos (`textarea` principal, modales y `textinput`).
  - Nuevo test unitario `TestViewIsAlwaysOpaque` para verificar que cada línea generada por `View()` tenga el escape de fondo opaco garantizado.
- `install.sh` detecta Git Bash/MSYS/Cygwin y remite a `install.ps1` en vez de fallar.

## [0.5.0] - 2026-10-27

Primera versión publicada en GitHub. Añade empaquetado y distribución:
`mkd --version`, releases multiplataforma con goreleaser, instalador `curl | sh`,
Makefile, CI y documentación.

### Corregido

- **Editor de sub-agentes: el footer de acciones ya no desaparece.** El modal crecía con el textarea del
  prompt y `lipgloss` no recorta, así que `[Ctrl+S] Guardar / [Esc] Volver` quedaba fuera de pantalla
  ("no me deja bajar a la opción de abajo").
  - `modalBoxWithFooter` reserva la altura del footer, recorta el cuerpo y garantiza el recorte final.
  - `promptViewForEditor` mide en dos pasadas el alto ocupado por el resto del formulario (sin
    constantes frágiles) y limita el prompt al hueco real.
  - Navegación: `↓`/`↑` cambian de campo; desde el prompt multilínea se sale con `Tab` o
    `Ctrl+↓`/`Ctrl+↑`, para no quedar atascado.
  - Test de regresión en 140×45, 120×30 y 100×24 comprobando footer visible y modal dentro de pantalla.


- **Carga automática del catálogo de modelos al configurar la API key.** Antes había que pulsar
  `Ctrl+F` a mano para sincronizar con `/v1/models`; ahora, al abrir el selector sin caché previa
  para el proveedor activo, se sincroniza solo.
- **Catálogo filtrado para DeepInfra.** Se consulta `/models/list` (397 entradas mezclando
  imagen, embeddings y deprecados) y se conservan solo los modelos `text-generation` activos y
  no privados (~96), evitando un selector inmanejable. Otros proveedores siguen usando
  `/v1/models`.

- **Navegación entre campos con flechas en el editor de sub-agentes.** En terminales
  embebidas (VS Code, JetBrains) `Tab` cambia de panel antes de llegar a la TUI, así que el
  editor del roster ahora avanza/retrocede con `↑/↓` además de `Tab`/`Shift+Tab`. En el prompt
  multilínea las flechas siguen moviendo el cursor; `PgUp`/`PgDn` cambian de campo.

### Motor de Orquestación Multi-Agente (Swarm Engine) (Fase 4)

- **Perfiles de sub-agente configurables** (`internal/config`, `internal/swarm/profiles.go`):
  nuevo mapa `sub_agents` en `config.json` con `SubAgentProfile`
  (`id`, `name`, `icon`, `color`, `role_prompt`, `provider`, `model`, `allowed_tools`, `enabled`).
  Cuatro perfiles preinstalados y editables: `explorer` (Code Explorer, cian), `coder`
  (Senior Implementer, púrpura), `qa` (QA & Security Auditor, ámbar) y `docs` (Tech Writer,
  verde, deshabilitado por defecto). Un `provider`/`model` vacío hereda el global.
- **Modal Gestor de Equipo** (`Ctrl+T` / `/agents`, `internal/ui/agentmodal.go`): roster con
  activar/desactivar (`Space`), editor de Nombre/Icono/Color/Modelo/Tools/Prompt por agente,
  alta (`Ctrl+N`) y baja (`Ctrl+D`), todo persistido en `config.json`.
- **Motor de orquestación** (`internal/swarm/orchestrator.go`):
  - El Orquestador recibe el roster habilitado en su system prompt y herramientas exclusivas
    `delegate_task(agent_id, task_title, instructions, context_files)` y
    `delegate_parallel(tasks[])`.
  - Cada delegación corre en su propia goroutine con `sync.WaitGroup` (pool acotado a 4),
    cliente scoped (provider/model del perfil), **contexto sandboxed** (mensajes nuevos, sin
    el historial del Orquestador) y catálogo de tools filtrado por `allowed_tools`
    (con soporte de `*` y `mcp:*`). El Orquestador solo recibe el reporte final.
  - Tope de rondas por sub-agente, timeout por delegación y cancelación global.
- **Concurrencia y HITL** (`internal/swarm/events.go`): `EventBus` no bloqueante
  (`chan SwarmEvent`) con `EventAgentStarted/StreamChunk/ToolCall/ToolResult/Permission/
  Completed/Error`; `HITLBroker` serializa los permisos con un mutex y recuerda las
  aprobaciones "siempre" por agente+tool. El modal de Diff muestra
  `🛡️ Sub-Agente [coder] solicita patch_file`.
- **Swarm Mission Control** (`internal/ui/swarmview.go`):
  - Barra de pestañas bajo el header: `[0: Orquestador] [1: explorer ✓] [2: ⠋ coder] …`,
    con `Alt+0..9` para alternar entre el chat consolidado y el inspector en vivo de cada
    sub-agente.
  - Sidebar derecho con el árbol `SWARM MISSION CONTROL` (orquestador + agentes, estado,
    duración/tokens y última herramienta ejecutada).
  - Tarjetas de delegación en el chat del Orquestador, con el borde del color del sub-agente.
- **Comandos y atajos**: `/swarm <tarea>` (misión orquestada), `/agents` y `Ctrl+T` (gestor de
  equipo), `Alt+S` (alterna píldora `SINGLE AGENT` ↔ `SWARM ORCHESTRATOR` en la barra de
  estado), `Alt+0..9` (inspector). En modo SWARM, un mensaje libre se trata como misión.
  `Esc` cancela la misión y rechaza los permisos pendientes.
- Tests: `internal/swarm` (perfiles, `allowed_tools`, parseo de delegaciones, bus no
  bloqueante, broker HITL, sandboxing, filtrado de tools, aprobación/rechazo y ejecución
  paralela con clientes falsos) y `internal/ui` (render de roster, toggle/edición, pestañas,
  árbol del sidebar, permiso HITL y una misión completa de extremo a extremo contra un
  proveedor OpenAI-compatible simulado).

### Integración Git nativa y cliente MCP (Fase 3)

- **`/commit` + `Ctrl+G`** (`internal/git`, `internal/ui/gitmodal.go`): flujo inteligente que
  inspecciona `git status --porcelain`, `git diff --staged` y `git diff`, envía el diff
  truncado por archivo/hunk junto a los 3 últimos `git log --oneline` al LLM y abre un modal
  con `bubbles/textarea` precargado con el **Conventional Commit** propuesto.
  - `Ctrl+Enter` ejecuta `git commit -m` (con `git add -u`/`-A` automático si no hay stage) y
    muestra hash + resumen en el chat y la barra de estado.
  - `Ctrl+R` regenera el mensaje, `Ctrl+A` hace `git add -A`, `Esc` cancela.
  - Aviso explícito `⚠ No hay archivos en stage…` cuando solo hay cambios sin stage.
- **`/review [rama-base]`** (`internal/ui/review.go`): code review local en streaming sobre
  `git diff -U5` contra los cambios actuales o contra la rama indicada, con prompt de Senior
  Code Reviewer estructurado en 🔴 Bugs/Condiciones de Carrera/Fugas, 🟡 Seguridad/Errores/
  Casos Borde y 🟢 Refactorización idiomática con bloques listos para `Ctrl+Y`.
- **Cliente MCP nativo** (`internal/mcp`, sin dependencias nuevas):
  - `client.go`: JSON-RPC 2.0 sobre `stdin`/`stdout` con `bufio.Scanner` de buffer ampliado
    (8 MiB), mapa de canales por `id` para peticiones concurrentes, handshake
    `initialize` (`protocolVersion: 2024-11-05`) + `notifications/initialized`,
    `tools/list`, `tools/call` y `Close()` ordenado (SIGTERM/espera/Kill).
  - `manager.go`: orquesta varios servidores, convierte sus tools a `openai.Tool` con prefijo
    `mcp__<server>__<tool>`, enruta llamadas y expone `Statuses()` para la TUI.
  - Las tools MCP pasan por el mismo HITL que las nativas salvo `auto_approve: true` en el
    servidor o aprobación `[A]` de la sesión; el chat pinta `🔌 MCP [server] -> tool`.
- **Config** (`internal/config`): nuevo mapa `mcp_servers` con `MCPServerConfig`
  (`command`, `args`, `env`, `enabled`, `auto_approve`) y un ejemplo deshabilitado de
  `@modelcontextprotocol/server-filesystem` en la configuración inicial.
- **Gestor MCP en la TUI**:
  - `Ctrl+O` / `/mcp` (`internal/ui/mcpmodal.go`): lista con indicador ●/○, nº de tools,
    activar/desactivar (`Space`/`Enter`), reconectar (`Ctrl+R`) y alta guiada
    (`Ctrl+N` → Nombre/Comando/Args → `Ctrl+S`) persistida en `config.json`.
  - Sidebar derecho: sección `🔌 SERVIDORES MCP` con estado y número de herramientas.
  - Arranque paralelo de los servidores habilitados en `Init` y cierre en `quit`/Ctrl+C.
- Tests: `internal/git` (repo temporal real), `internal/mcp` (servidor MCP falso vía
  `TestMain`), y en `internal/ui` render de los modales de commit/MCP, sección del sidebar,
  unificación de tools y enrutado/HITL de tools MCP.

### Añadido

- **Sistema de reglas** (`internal/rules`): archivos Markdown con frontmatter
  (`description`, `globs`, `alwaysApply`, `priority`, `name`) que se inyectan en el system prompt como
  `<project_rules>`.
  - Descubrimiento en cascada: `~/.config/mkd/rules/` (global, configurable con `rules_dir` /
    `MKD_RULES_DIR`), `<raíz>/.mkd/rules/` y `.mkd/rules/` en cada subcarpeta hasta el cwd.
  - Compatibilidad con `MKD.md` (raíz y subcarpetas), que pasa a ser una regla `alwaysApply` llamada
    `mkd` / `mkd-<subcarpeta>`.
  - Aplicación: `alwaysApply` siempre; `globs` cuando algún archivo anclado o ruta de la petición casa
    (soporta `*`, `?` y `**`, estilo .gitignore para patrones sin `/`); el resto son manuales.
  - Forzado manual con `:@nombre` en el popup de menciones (icono `⚖`, se combina con las skills),
    con marca `✓` y feedback verde al activarla.
  - Comandos `/rules` (listar con su disparador), `/rule rm <nombre>` (liberar) y
    `/rules-dir <ruta>` (carpeta global persistente).
  - Orden estable por prioridad y ruta para no invalidar la caché de prefijo.
  - Tests de frontmatter (inline y listas YAML), cascada, `MKD.md` legacy, matching de globs,
    selección por prioridad, render del bloque e integración en la TUI.

- **Sistema de skills** (`internal/skills`): discovery de carpetas `<skills_dir>/<nombre>/SKILL.md`,
  parser de frontmatter (`description:`), validación de nombres, instalación/sobrescritura/borrado y
  `Block()` para inyectar `<skill name="…">…</skill>` al contexto. Carpeta por defecto
  `~/.local/share/mkd/skills`, configurable con `skills_dir` en la config o `MKD_SKILLS_DIR`.
- **Invocación con `:@nombre`**: el popup de menciones detecta el trigger `:@`, lista solo skills con
  filtro difuso, marca las ya activas y `Enter` las inyecta en el contexto y las registra en el sidebar
  con estilo propio (ámbar, icono `✦`).
- **Feedback visual de selección confirmada**: al anclar un archivo, skill o mención especial aparece
  `✓ seleccionado: …` en verde sobre el input durante 3 segundos, además de la marca `✓` en el sidebar.
- Comandos `/skills` (listar), `/skill add <nombre>` (editor que crea el `SKILL.md` con `Ctrl+S`),
  `/skill rm <nombre>` y `/skills-dir <ruta>` (persistente en config).
- Tests de skills (scan, frontmatter, install/overwrite/remove, nombres inválidos, `~`), del trigger
  `:@`, de la inyección al contexto, del banner de confirmación y de la no regresión del trigger `@`.

### Corregido

- **Carga automática del catálogo de modelos al configurar la API key.** Antes había que pulsar
  `Ctrl+F` a mano para sincronizar con `/v1/models`; ahora, al abrir el selector sin caché previa
  para el proveedor activo, se sincroniza solo.
- **Catálogo filtrado para DeepInfra.** Se consulta `/models/list` (397 entradas mezclando
  imagen, embeddings y deprecados) y se conservan solo los modelos `text-generation` activos y
  no privados (~96), evitando un selector inmanejable. Otros proveedores siguen usando
  `/v1/models`.

- **Menú de menciones `@` tipo terminal.** El autocompletado ahora funciona por ruta:
  `@internal/ai/` lista los hijos de la carpeta y `@internal/ai/cl` resuelve
  `internal/ai/client.go`. Los **directorios salen primero**, con `/` y etiqueta
  "directorio"; `Tab` o `Enter` sobre una carpeta descienden a su contenido sin cerrar el
  popup, y la ruta completa se muestra en el input y en el sidebar.
- **`Enter` ya no abre el modal de modelos.** El popup de menciones ahora tiene prioridad
  absoluta sobre el texto del input, así que `Enter` ancla el archivo elegido en lugar de
  enviar el mensaje o abrir otro modal.
- **Sin modal al arrancar.** Si el proveedor tiene modelos guardados pero ningún modelo
  activo, se selecciona el primero en lugar de abrir el gestor de modelos al terminar el
  splash.

### Integración TUI (Fase 2)

- **Modos de agente** (`chat`, `plan`, `default`, `auto`) en `config.agent_mode`, rotados con
  `Ctrl+T` y seleccionables con `/mode <plan|default|auto|chat>`; badge `⟨MODO⟩` en la barra de estado.
  - `plan`: solo lectura/búsqueda; `write_file`/`patch_file`/`run_command` se bloquean y se reporta
    el bloqueo al modelo para que describa el plan en texto.
  - `default`: HITL para escritura y comandos (`Y`/`Enter` aplica, `N`/`Esc` rechaza, `A` permite
    siempre en la sesión).
  - `auto`: aplica escritura y comandos dentro del `cwd` sin preguntar, avisando con `◈ auto: …`.
  - `chat`: modo conversacional sin herramientas (comportamiento previo a esta fase).
- **Bucle de agente** (`internal/ui/agentloop.go`) sobre `CompleteMessagesTools`: ventana con
  `role=tool`, autocorrección de errores de argumentos, tope de 50 rondas, resultados resumidos en el
  chat y reutilización del resumen/archivos anclados del `ContextManager`.
- `Ctrl+Y` / `/copy` copia el último bloque de código con aviso `✓ Bloque copiado al portapapeles (N líneas)`.
- `Alt+C` / `/blocks` abre el selector de bloques (Enter/1-9 copiar, `Ctrl+S` guardar, `Ctrl+R`
  validar ejecución shell).
- Menciones `@` cableadas en el `Update`/`View`: popup sobre el input, `↑/↓`, `Tab` completar y
  `Enter` aplicar; los archivos se registran en el sidebar y los bloques especiales
  (`@git:diff`, `@git:log`, `@tree`) se inyectan al enviar.
- Tests de integración de la TUI: bloqueo en modo plan, rechazo HITL reportado al modelo,
  `[A]` persistente en sesión, `Enter` sobre `@archivo` en el sidebar y ciclo de modos.

### Añadido

- `internal/clipboard`: copia al portapapeles en cascada (OSC 52 con passthrough tmux/screen, `pbcopy`,
  `wl-copy`, `xclip`, `xsel`) usando solo stdlib; `Available()` lista los backends detectados.
- `internal/ui/codeactions.go`: extracción de bloques de código markdown con ruta sugerida (comentario
  inicial o cabecera previa), `langExtension`, modal `blocksModal` (copiar/guardar/ejecutar con acciones
  tipadas `blockAction`) y helper `copyBlock` sobre el portapapeles.
- **Tool calling + HITL + diff viewer** (`internal/agent`): catálogo `Tools` (`read_file`, `list_dir`,
  `search_code`, `write_file`, `patch_file`, `run_command`) con JSON Schema, `BuildProposal` (valida rutas
  con `SafePath`, calcula diff y decide auto-aprobación), `Execute` (escritura atómica tmp+rename, patch
  de coincidencia única, shell con timeout), `UnifiedDiff` por LCS con degradación a diff trivial para
  secuencias > 4000 líneas, `search_code` con ripgrep o walker concurrente (8 workers, `.gitignore`
  básico, 1 MiB, detección de binarios) y mensajes `ResultMessage`/`RejectedMessage`.
- `internal/ui/diffmodal.go`: `modalDiff` con cabecera `🛡️ Permiso de Agente Requerido: <tool>`, diff
  coloreado (+verde / -rojo), `decisionForKey` puro (Y/Enter, N/Esc, A) y router `updateDiffModalKey`.
- `ai.Client.StreamWithTools` / `CompleteMessagesTools`: tool calling aditivo sin cambiar
  `Stream`/`Complete`.

## [0.4.0] - 2026-10-26

### Añadido

- **Menciones @ con fuzzy finder** (`internal/context/providers.go`, `internal/ui/mentions.go`):
  - Proveedores especiales `@git:diff` (unstaged + staged en `<git_diff>`), `@git:log` (últimos 5
    commits con `--stat` en `<git_log>`) y `@tree` (árbol de 3 niveles con guías `├──/└──/│`, en
    `<project_tree>`).
  - `ResolveMention(token, cwd)` resuelve menciones especiales o lee archivos (límite 1 MiB y
    rechazo de binarios) devolviendo `<file path="…">…</file>`, mismo estilo que el manager.
  - `FileIndex` con `Build/Match` sobre walk que respeta `.gitignore` (patrones, negación `!`,
    `/` inicial, `*`, `**`, defaults `.git`, `node_modules`, `vendor`), ignora binarios por
    extensión y por NUL en los primeros 512 bytes, y topa en 2000 entradas. Caché invalidada por
    mtime+tamaño del `.gitignore`.
  - `fuzzyScore` de subsecuencia case-insensitive con bonus por inicio de segmento, coincidencia
    contigua y penalización por longitud/posición.
  - Popup flotante `@ contexto` (máx. 8 filas) con especiales primero y archivos después; `↑/↓`
    navega, `Tab` completa el token, `Enter` adjunta el archivo al contexto o guarda el bloque
    especial. Sin perder el foco del textarea.
- Tests de `fuzzyScore`, indexador con `.gitignore`, árbol a 3 niveles, parser de patrones y
  flujo del popup de menciones.

## [0.3.0] - 2026-10-26

### Añadido

- **Motor de contexto**(`internal/context`) con ensamblado optimizado para Prefix Prompt Caching:
  orden estricto `system estático → <context_files> → <context_summary> → historial`, sin timestamps dinámicos.
- Detección automática de `MKD.md` en el directorio de trabajo; se anexa al system prompt como
  `<project_context>` y se cachea hasta que cambie el system prompt.
- **Archivos anclados** con hash SHA-256, releídos del disco en cada petición y empaquetados como
  `<context_files>`; el sidebar marca `✓` / `↻ cambiado` / `! sin acceso` y se avisa en la conversación
  cuando un archivo cambia en disco.
- Desglose de tokens (`SystemTokens`, `FileTokens`, `SummaryTokens`, `HistoryTokens`, `TotalTokens`) con
  barra de uso frente al umbral y porcentaje en el sidebar.
- **Auto-compactación**: `/compact` o disparo automático al superar `compact_threshold` (por defecto 60,000).
  Resume el 70% más antiguo, conserva los últimos 4 mensajes intactos y reporta
  `✓ Contexto compactado: 18,400 -> 1,200 tokens`.
- `internal/storage`: sesiones en `~/.local/share/mkd/sessions/<id>.json` con título autogenerado desde el
  primer prompt, `cwd`, proveedor/modelo, archivos, resumen y mensajes; guardado atómico tras cada turno y al salir.
- **Modal de sesiones** (`Ctrl+H` o `/sessions`): listado por fecha, reanudar con `Enter`, nueva con `Ctrl+N`,
  eliminar con `Ctrl+D` y recargar con `Ctrl+R`.
- Caché de catálogo de modelos en `~/.local/share/mkd/models_cache.json`: `Ctrl+M` abre al instante desde
  caché y solo `Ctrl+F` reescribe el archivo tras consultar `/v1/models`.
- Comandos `/new`, `/title <texto>` y `/remove <ruta>`; títulos y barra de uso visibles en header y sidebar.
- Claves `compact_threshold` (config) y `MKD_COMPACT_THRESHOLD`/`IA_COMPACT_THRESHOLD` (entorno).
- Métodos `StreamMessages` / `CompleteMessages` en el cliente IA para enviar mensajes ya ensamblados.
- Tests para ensamblado, split de compactación, detección de cambios por hash, sesiones, títulos y caché de modelos.

### Conservado

- Modo pipe/one-shot, configuración multi-proveedor y resto de modales sin cambios de contrato.

## [0.2.0] - 2026-10-26

### Añadido

- Configuración multi-proveedor y multi-modelo (`ProviderConfig` + `Config.Providers`) con perfiles iniciales para DeepInfra, OpenRouter, OpenAI y Ollama.
- Migración automática del esquema plano anterior (`api_key`, `base_url`, `model`) al nuevo esquema.
- Splash screen pixel art a ~60 FPS con logo MKD, mascota mecha, barrido neón, gradiente púrpura/cian/verde y barra de progreso; saltable con cualquier tecla.
- Onboarding automático al terminar el splash cuando el proveedor activo no tiene API key (excepto Ollama/local).
- Workspace full-screen con header, conversación, sidebar de proveedor/archivos/telemetría, input multilínea y barra de estado.
- Modales superpuestos con `cellbuf` y bordes dobles:
  - Proveedor/API key (`Ctrl+K` o `/keys`), con selector de proveedor, URL, key enmascarada y `Ctrl+U` para mostrar/ocultar.
  - Gestor multi-modelo (`Ctrl+M`/`F2`/`Ctrl+O` o `/model`), con filtro, selección, alta (`Ctrl+A`), baja (`Ctrl+D`), sincronización con `/v1/models` (`Ctrl+F`) y cambio de proveedor (`Ctrl+←/→`).
  - Editor de system prompt (`/system`) con textarea y `Ctrl+S`.
  - Paleta de comandos (`Ctrl+P`) con búsqueda y acciones.
- Comandos slash: `/model`, `/model add <id>`, `/keys`, `/connect`, `/add <ruta>`, `/system`, `/compact`, `/clear`, `/help`.
- Autocompletado flotante al escribir `/` como primer carácter: lista filtrable de comandos con descripción, navegación con flechas, `Tab` para completar y `Enter` para ejecutar.
- Modales de tamaño fijo (misma anchura y altura en todos los estados) para evitar saltos al cambiar de proveedor, filtrar modelos o abrir otro modal.
- Compactación de conversación mediante resumen no streaming y reinyección del resumen en el system prompt.
- Archivos de contexto adjuntos con `/add` o `-f`, visibles en el sidebar y enviados como bloques XML.
- `FetchAvailableModels` en el cliente IA para sincronizar modelos desde `GET /v1/models`.
- Cancelación de streaming con `Esc`, spinner en la cabecera del mensaje en curso y notas de error/cancelación.
- Atajos `Ctrl+B` (sidebar), `Ctrl+L` (limpiar), `Ctrl+P` (paleta), `Ctrl+M`/`F2` (modelos), `Ctrl+K` (keys).
- El workspace arranca sin API key ni URL y muestra «Ningún modelo disponible actualmente» con acceso directo a la configuración.
- Módulo y binario renombrados a `mkd`; `go install .` produce `mkd`.

### Conservado

- Modo pipe/one-shot intacto: `cat archivo | mkd "prompt"`, `mkd ask`, `-f`, `-s`, `-p` y salida directa a stdout.
- Configuración en `$XDG_CONFIG_HOME/mkd/config.json` con permisos `0600` y variables `IA_*`/`MKD_*`.

## [0.1.0] - 2026-10-26

- Primera versión funcional con modo pipe, one-shot, TUI básica, streaming SSE y configuración de un solo proveedor.
