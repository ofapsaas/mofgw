# Publication Policy — mofgw public repo

> **Regla de hierro (decisión del dueño, 18 Ago 2026):** este repositorio es **público y GPL**. Todo lo que se escriba aquí — el árbol de trabajo actual **y el historial** — debe ir **siempre sanitizado**.

## Lo que NUNCA va a este repo

- Rutas reales del host/workspace: `/home/<user>`, `$HOME`, rutas absolutas locales.
- Nombres de cuentas/providers internos: `go-provider*`, `go-provider-c`, `go-provider-b`, `go-corp`, `go-fiel`.
- Clientes / credenciales de prueba internos: `client-x`, `client-opencode`, `<test-key>`.
- Infraestructura del operador: `infra-dev`, `test-instance`, `internal.saas.arar`, IPs privadas (`51.*`, `10.*`, `192.168.*`), hostnames internos.
- Keys, tokens, secrets, límites de cuota o billing de providers.

## Placeholders a usar en su lugar

`$HOME` · `<test-key>` · `provider-1` / `go-provider-*` · `<client>` · `<infra-host>`

## Checker antes de pushear

```bash
# árbol actual
git grep -nE "/home/[a-z]|go-provider|go-provider-c|go-provider-b|<test-key>|client-x|infra-dev|test-instance|ofap\.saas|51\.[0-9]+\.[0-9]+\.[0-9]+" -- ':!*.resp.json' || echo "arbol limpio"

# historial completo (todos los commits)
git rev-list --all | while read c; do git grep -lE "/home/[a-z]|<test-key>|infra-dev|go-provider-c" "$c" 2>/dev/null; done | sort -u
```

## Si una fuga ya se publicó en el historial

1. `git filter-repo` sobre un clone/copia de trabajo (eliminar la fuga del historial, no solo del árbol).
2. Verificar con el checker que no queden ocurrencias en ningún commit.
3. Backupear el estado previo con `git bundle create`.
4. Force-push (`git push --force`).

## Incidencia de referencia (18 Ago 2026)

El historial público contenía `$HOME` (≈47 commits), `<test-key>` (≈142) y archivos internos del worker (`task_plan.md`, `.cdad-state.json`, `external-reviews/*.resp.json`). Se reescribió el historial completo con `git-filter-repo` (limpieza de strings + eliminación de archivos internos) y se force-pusheó. El commit final limpio quedó en `6747371`. Backup previo: bundle local del 2026-08-18. **Esta política existe para que no se repita.**
