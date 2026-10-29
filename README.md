<div align="center">

# MKD

**Asistente de IA para tu terminal.**

TUI completa, modo pipe y one-shot, agente con tool calling, sub-agentes, skills y reglas de proyecto.

[![CI](https://github.com/Yak036/mkd/actions/workflows/ci.yml/badge.svg)](https://github.com/Yak036/mkd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Yak036/mkd?sort=semver)](https://github.com/Yak036/mkd/releases/latest)
[![Go](https://img.shields.io/badge/go-1.27%2B-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

</div>
---

## Qué es

`mkd` es un CLI escrito en Go que habla con cualquier proveedor compatible con
OpenAI (DeepInfra, OpenRouter, OpenAI, Ollama…) y cubre tres formas de uso:

| Modo | Cuándo | Ejemplo |
|------|--------|---------|
| **Pipe** | Quieres analizar stdin | `cat main.go \| mkd "optimiza esto"` |
| **One-shot** | Una respuesta y salir | `mkd -f go.mod "revisa las dependencias"` |
| **TUI** | Trabajo conversacional largo | `mkd chat` |

En la TUI tienes agente con herramientas (`read_file`, `search_code`,
`patch_file`, `run_command`…), aprobación HITL con visor de diffs, sub-agentes,
servidores MCP, skills y reglas de proyecto.

---

## Instalación

### Binario precompilado (recomendado)

Descarga el archivo de tu plataforma desde
[**Releases**](https://github.com/Yak036/mkd/releases/latest) y muévelo al `PATH`:

```bash
# Linux amd64 (ajusta la versión y la arquitectura)
curl -fLo mkd.tar.gz \
  https://github.com/Yak036/mkd/releases/latest/download/mkd_Linux_x86_64.tar.gz
tar -xzf mkd.tar.gz mkd
sudo install -m 0755 mkd /usr/local/bin/mkd
mkd --version
```

### Script de instalación

**Linux y macOS**

```bash
curl -fsSL https://raw.githubusercontent.com/Yak036/mkd/main/install.sh | sh
```

Detecta SO y arquitectura, descarga la última release y la instala en
`/usr/local/bin` (o `~/.local/bin` sin permisos de root). Variables:
`MKD_VERSION` (fijar versión), `MKD_INSTALL_DIR` (directorio destino).

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/Yak036/mkd/main/install.ps1 | iex
```

Descarga `mkd.exe`, verifica el checksum SHA-256, lo instala en
`%LOCALAPPDATA%\mkd` y añade esa carpeta al `PATH` del usuario. No requiere
administrador ni WSL. Abre una terminal **nueva** después para que el `PATH`
tenga efecto. Mismas variables: `MKD_VERSION`, `MKD_INSTALL_DIR`.

> En Windows, `run_command` del agente usa **PowerShell** (o `cmd.exe` si no
> está disponible), y el portapapeles usa `clip.exe`.

### Con Go

```bash
go install github.com/Yak036/mkd@latest
```

Requiere Go 1.27 o superior. El binario queda en `$(go env GOPATH)/bin`.

### Desde el código

```bash
git clone https://github.com/Yak036/mkd.git
cd mkd
make install          # compila e instala en ~/.local/bin
# o
go build -trimpath -ldflags "-s -w" -o mkd .
```

### Homebrew (macOS / Linux)

```bash
brew install Yak036/tap/mkd
```

### Arch Linux (AUR)

```bash
yay -S mkd-bin
```

---

## Primeros pasos

```bash
# 1. Configura un proveedor y su API key
mkd config set deepinfra --key sk-... --model deepseek-ai/DeepSeek-V4.1-Flash-1017

# 2. Comprueba que quedó bien
mkd config show

# 3. A la TUI
mkd chat
```

La configuración vive en `~/.config/mkd/config.json` (respeta `XDG_CONFIG_HOME`)
y se puede sobreescribir con variables de entorno.

### Proveedores incluidos

| Proveedor | Base URL | Notas |
|-----------|----------|-------|
| `deepinfra` | `https://api.deepinfra.com/v1/openai` | Por defecto |
| `openrouter` | `https://openrouter.ai/api/v1` | |
| `openai` | `https://api.openai.com/v1` | API Key de desarrollador |
| `codex` | `https://chatgpt.com/backend-api/codex` | Suscripción ChatGPT Plus/Pro/Team (OAuth o ~/.codex) |
| `claude` | `https://api.anthropic.com/v1` | Suscripción Claude Pro/Team o token setup (`sk-ant-oat...`) |
| `antigravity` | `local://agy` | CLI local de Google Antigravity (`agy`) y cuenta Google activa |
| `ollama` | `http://localhost:11434/v1` | Sin API key |

```bash
# Autenticación con Google Antigravity (CLI agy local):
mkd auth antigravity       # Detecta automáticamente tu cuenta Google y sincroniza 13 modelos

# Autenticación con tu suscripción de Claude (cuota plana Pro/Team/Enterprise):
mkd auth claude           # Inicia sesión en el navegador con OAuth 2.0 PKCE
mkd auth claude --import  # O importa credenciales existentes de Claude Code (~/.claude)
mkd auth claude --token "sk-ant-oat..." # O asigna un setup-token directamente

# Autenticación con tu suscripción de Codex / ChatGPT (cuota plana):
mkd auth codex           # Inicia sesión en el navegador con OAuth 2.0 PKCE
mkd auth codex --import  # O importa tu sesión local de ~/.codex/auth.json
mkd auth status          # Comprueba el estado de las suscripciones y tokens
```

```bash
mkd config set miproveedor --url https://... --key sk-... --model modelo-x
mkd config use miproveedor
```

---

## Uso

### Pipe

```bash
git diff | mkd "escribe un mensaje de commit en Conventional Commits"
cat error.log | mkd "¿qué está fallando?"
```

### Archivos como contexto

```bash
mkd -f main.go -f internal/ai/client.go "¿dónde se pierde el contexto del stream?"
```

`-f` acepta rutas repetidas y también funciona con `-` para stdin.

### TUI

```bash
mkd chat
```

---

## La TUI

```
┌──────────────────────────────────────────────────────────────┐
│ ▓▒░ MKD ░▒▓   Sesión Activa              ● DeepInfra │ model │
├──────────────────────────────────────┬───────────────────────┤
│  ❯ Escribe un mensaje…               │ PROVEEDOR ACTIVO      │
│                                      │ ARCHIVOS EN CONTEXTO  │
│                                      │ TELEMETRÍA (tokens)   │
│                                      │ SESIÓN                │
└──────────────────────────────────────┴───────────────────────┘
```

### Atajos

| Tecla | Acción |
|-------|--------|
| `Ctrl+P` | Paleta de comandos |
| `Ctrl+M` / `F2` | Selector de modelos |
| `Ctrl+K` | Proveedor y API key |
| `Ctrl+H` | Historial de sesiones |
| `Ctrl+T` | Modo de agente / equipo de sub-agentes |
| `Ctrl+G` | Generar commit |
| `Ctrl+Y` | Copiar último bloque de código |
| `Alt+C` | Selector de bloques de código |
| `Ctrl+B` | Mostrar/ocultar sidebar |
| `Esc` | Cancelar generación / cerrar modal (también cancela una pregunta) |

### Comandos de barra

| Comando | Descripción |
|---------|-------------|
| `/model` | Cambiar, agregar o sincronizar modelos |
| `/keys` | Configurar proveedor y API key |
| `/add <ruta>` | Adjuntar archivo al contexto |
| `/blocks`, `/copy` | Bloques de código detectados / copiar el último |
| `/compact` | Resumir la conversación y liberar contexto |
| `/sessions`, `/new` | Historial de sesiones / sesión limpia |
| `/commit`, `/review` | Commit asistido y code review del diff |
| `/mcp` | Gestor de servidores MCP |
| `/agents`, `/swarm` | Sub-agentes y misiones multi-agente |
| `/skills`, `/skill add` | Skills instaladas y creación |
| `/rules`, `/rules-dir` | Reglas del proyecto |
| `/mode` | `plan` · `default` · `auto` · `chat` |
| `/help` | Ayuda |

---

## Funcionalidades

### Agente con herramientas

Cuatro familias de herramientas con aprobación **human-in-the-loop**:

- `read_file` / `list_dir` — lectura, auto-aprobada dentro del proyecto.
- `search_code` — usa `rg` si está instalado, si no un walker propio con `.gitignore`.
- `write_file` / `patch_file` — propone cambios y muestra el **diff coloreado** antes de aplicar.
- `run_command` — ejecuta con timeout y devuelve stdout+stderr al modelo para autocorregirse.
- `ask_user` — el agente **pregunta durante su razonamiento** con 2-6 opciones y una
  respuesta libre; el usuario elige con `↑/↓` o `1-6`, escribe la suya con `Tab` o
  cancela con `Esc`, y el mismo turno continúa con esa decisión.

### Modos

| Modo | Comportamiento |
|------|----------------|
| `chat` | Conversación sin herramientas |
| `plan` | Solo lectura: propone, no escribe |
| `default` | Pide permiso para escribir y ejecutar |
| `auto` | Auto-aprueba dentro del proyecto |

### Contexto y caché

- Ensamblado estable `system → <context_files> → <context_summary> → historial`
  para maximizar el *prefix prompt caching*.
- El resumen de compactación viaja **una sola vez** (bloque `<context_summary>`), nunca
  duplicado en el historial.
- Un turno a la vez: mientras el agente genera, espera un permiso o una pregunta, un
  mensaje nuevo se rechaza con aviso en vez de entrelazar dos conversaciones.
- Archivos anclados con hash SHA-256, recargados si cambian en disco.
- Auto-compactación al superar el umbral de tokens (60 000 por defecto).
- Menciones `@` con autocompletado por ruta (`@internal/ai/`) y especiales
  `@git:diff`, `@git:log`, `@tree`.

### Reglas de proyecto

```
.mkd/rules/*.md        # específicas del repo, en cascada por subcarpeta
~/.config/mkd/rules/   # globales
```

```markdown
---
description: Convenciones de Go del equipo
globs: internal/**/*.go
alwaysApply: false
priority: 10
---

Usa errores envueltos con %w y nombres cortos.
```

Invócalas con `:@nombre-regla` o deja que los `globs` decidan.

### Skills

```
~/.local/share/mkd/skills/<nombre>/SKILL.md
```

```markdown
---
description: Revisa código Go idiomático
---
Revisa el diff y señala problemas de concurrencia, errores ignorados y fugas.
```

Úsalas con `:@nombre-skill` o créalas con `/skill add <nombre>`.

### Servidores MCP

Configura servidores en `mcp_servers` de `config.json` o desde `/mcp`
(`Ctrl+O`). Sus herramientas se integran en el mismo bucle de tool calling.

```json
{
  "mcp_servers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/ruta/vault"],
      "enabled": true,
      "auto_approve": false
    }
  }
}
```

---

## Actualizar

`mkd` comprueba una vez al día si hay versión nueva y lo muestra en la barra de
estado: `↑ v0.7.0 disponible · mkd upgrade`.

```bash
mkd upgrade --check    # ¿hay versión nueva?
mkd upgrade            # descarga, verifica checksum y se reemplaza
```

Si instalaste con el script, también puedes repetirlo:

```bash
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Yak036/mkd/main/install.sh | sh

# Windows
irm https://raw.githubusercontent.com/Yak036/mkd/main/install.ps1 | iex
```

La comprobación automática se desactiva con `MKD_NO_UPDATE_CHECK=1`.

## Configuración

Archivo: `~/.config/mkd/config.json`

```json
{
  "active_provider": "deepinfra",
  "system_prompt": "Eres MKD, un asistente técnico senior…",
  "compact_threshold": 60000,
  "agent_mode": "default",
  "skills_dir": "~/.local/share/mkd/skills",
  "rules_dir": "~/.config/mkd/rules",
  "providers": {
    "deepinfra": {
      "name": "DeepInfra",
      "base_url": "https://api.deepinfra.com/v1/openai",
      "api_key": "sk-...",
      "active_model": "deepseek-ai/DeepSeek-V4.1-Flash-1017",
      "models": ["deepseek-ai/DeepSeek-V4.1-Flash-1017"]
    }
  }
}
```

### Variables de entorno

| Variable | Efecto |
|----------|--------|
| `MKD_PROVIDER` / `IA_PROVIDER` | Proveedor activo |
| `MKD_MODEL` / `IA_MODEL` | Modelo activo |
| `MKD_API_KEY` | API key del proveedor activo |
| `MKD_BASE_URL` | Base URL |
| `MKD_SYSTEM_PROMPT` | System prompt |
| `MKD_AGENT_MODE` | `chat` · `plan` · `default` · `auto` |
| `MKD_COMPACT_THRESHOLD` | Umbral de auto-compactación |
| `MKD_SKILLS_DIR` / `MKD_RULES_DIR` | Carpetas de skills y reglas |

---

## Desarrollo

```bash
make build     # binario en ./mkd
make test      # go test ./...
make lint      # go vet + gofmt
make run       # go run . chat
make install   # instala en ~/.local/bin
```

Ver [CONTRIBUTING.md](CONTRIBUTING.md) para el flujo de PRs y [CHANGELOG.md](CHANGELOG.md)
para el historial de cambios.

---

## Licencia

[MIT](LICENSE) © 2026 Ramces Vera
