# Publication Policy — mofgw public repo

> **Regla de hierro (decisión del dueño, 18 Ago 2026):** este repositorio es **público y GPL**. Todo lo que se escriba aquí — el árbol de trabajo actual **y el historial** — debe ir **siempre sanitizado**.

## Lo que NUNCA va a este repo

- Rutas reales del host/workspace: `/home/<user>`, rutas absolutas locales.
- Nombres reales de cuentas/providers internos (prefijos de cuenta del operador, ej. `go-provider-*` con sufijos internos).
- Clientes / credenciales de prueba internos (tokens de prueba, IDs de cliente del operador).
- Infraestructura del operador: hostnames/sufijos internos (`*.saas.ar`, hosts de dev/prod), IPs privadas (`10.*`, `192.168.*`, rangos públicos del operador), nombres de instancias internas.
- Keys, tokens, secrets, límites de cuota o billing de providers.

> **Nota:** no listar aquí strings literales de identidad real (hosts, cuentas, tokens reales). El propio policy no debe exponerlos. Usar descripciones genéricas y placeholders.

## Placeholders a usar en su lugar

`$HOME` · `<test-key>` · `provider-1` / `go-provider-*` · `<client>` · `<infra-host>`

## Checker antes de pushear

```bash
# árbol actual — revisar además cualquier string literal real que no deba salir.
# PATRONES: rutas reales /home/<user>, prefijos de cuentas internas del operador,
# tokens de prueba, hosts privados, sufijos de dominio interno, IPs privadas.
git grep -InE "/home/[a-z]|internal:test-key|go-internal|client-probe|infra-dev|test-instance|internal\.saas\.ar|[[:space:]]51\.[0-9]+\.[0-9]+\.[0-9]+" -- ':!*.resp.json' || echo "arbol limpio"
```

> El comando usa patrones **genéricos** (sin strings literales reales de identidad interna) para que él mismo no filtre información. En cualquier contenido publicable, preferir las descripciones genéricas de arriba antes que los strings literales.

## Si una fuga ya se publicó en el historial

1. `git filter-repo` sobre un clone/copia de trabajo (eliminar la fuga del historial, no solo del árbol).
2. Verificar con el checker que no queden ocurrencias en ningún commit.
3. Backupear el estado previo con `git bundle create`.
4. Force-push (`git push --force`).

## Incidencia de referencia (18 Ago 2026)

El historial público contenía rutas reales del host, un token de prueba interno y archivos internos del worker (`task_plan.md`, estado del worker, salidas de revisión). Se reescribió el historial completo con `git-filter-repo` (limpieza de strings + eliminación de archivos internos) y se force-pusheó, además de actualizar la documentación de usuario con las features 014 (registro unificado) y 015 (inter-attempt-delay). Backup previo: bundle local del 2026-08-18. **Esta política existe para que no se repita.**
