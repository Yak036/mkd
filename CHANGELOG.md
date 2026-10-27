# Changelog

Todas las modificaciones relevantes de MKD se documentan aquí.

## [Unreleased]

### Añadido

- **Soporte Windows completo**:
  - `install.ps1` para `irm ... | iex`: descarga `mkd.exe`, verifica checksum SHA-256,
    instala en `%LOCALAPPDATA%\mkd` y añade la carpeta al `PATH` del usuario (sin admin).
  - `run_command` usa **PowerShell** (`-NoProfile -NonInteractive`) y cae a `cmd.exe`,
    en lugar de `/bin/sh` que no existe en Windows.
  - Portapapeles con `clip.exe` como backend nativo.
  - `~\\ruta` se expande al home del usuario en `skills_dir`/`rules_dir`.
  - CI ejecuta la suite también en `windows-latest`.

### Corregido

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
