# Contribuir a MKD

Gracias por el interés. Esto es lo mínimo para que tu cambio entre sin fricción.

## Antes de abrir un PR

```bash
make lint    # go vet + gofmt
make test    # go test ./... -count=1
```

Ambos deben pasar. La CI ejecuta además `-race` en Linux y macOS.

## Flujo

1. Abre un issue describiendo el problema **o** el cambio propuesto. Para
   cambios grandes, coméntalo antes de escribir código.
2. Crea una rama desde `main`:
   ```bash
   git checkout -b feat/mi-cambio
   ```
3. Commits en **Conventional Commits**:
   ```
   feat(ui): añade selector de bloques con Alt+C
   fix(agent): evita que patch_file aplique sobre coincidencias ambiguas
   docs(readme): documenta las variables de entorno
   ```
4. PR contra `main` con:
   - Qué cambia y **por qué**.
   - Cómo lo probaste (comandos, capturas de TUI si aplica).
   - Si toca la UI, una nota de la terminal usada (tmux, VS Code, kitty…).

## Reglas del código

- **Sin dependencias nuevas** sin justificarlo en el PR.
- Mantén el estilo existente: comentarios en español, diagramas ASCII cuando
  aclaren un flujo, sin `// TODO` sueltos.
- Toda funcionalidad nueva lleva **tests**; los arreglos, un test de regresión.
- Errores envueltos con `%w` y mensajes accionables para el usuario.
- Nada de secretos en el repo: las claves van en `~/.config/mkd/config.json`.

## Estructura

```
cmd/                 comandos cobra (root, chat, config)
internal/ai/         cliente OpenAI-compatible y streaming
internal/agent/      herramientas, propuestas y diffs
internal/context/    ensamblado de contexto, compactación, menciones
internal/rules/      reglas de proyecto (.mkd/rules)
internal/skills/     skills instalables (~/.local/share/mkd/skills)
internal/storage/    sesiones y caché de modelos
internal/ui/         TUI (bubbletea): workspace, modales, sidebar
internal/mcp/        cliente MCP sobre stdio
internal/swarm/      sub-agentes y misiones
internal/version/    versión inyectada en build
```

## Releases

Los mantiene quien administra el repo:

```bash
git tag -a v0.4.0 -m "v0.4.0"
git push origin v0.4.0
```

El workflow `release.yml` compila todas las plataformas con goreleaser y
publica los artefactos.

## Licencia

Al contribuir aceptas que tu aporte se publique bajo la licencia [MIT](LICENSE).
