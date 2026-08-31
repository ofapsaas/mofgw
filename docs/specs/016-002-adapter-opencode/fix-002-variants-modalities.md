# Fix 016-002-002 — variants + modalities + attachment nativos (schema opencode real)

```yaml
context: |
  El cliente validó el fragmento contra el schema oficial de opencode
  (https://opencode.ai/config.json, descargado y verificado 31 Ago 2026):
  thinking NO es representable como array de niveles — el schema nativo usa
  `variants` (objeto {nivel: {}}); modalidades van en `modalities {input,
  output}` (ambos required) + `attachment: true` (booleano). Los x-* previos
  no existen en el schema (nivel modelo con additionalProperties: false).
  Causa raíz adicional: el renderer solo manejaba thinking como array, pero
  el IR (Meta desde modelCatalogEntry) trae thinking como OBJETO
  {"levels":[...], "default":"..."} — por eso el cliente veía 0 modelos con
  variants aunque 28 lo traen en /v1/models.
resolution: |
  modelFragment emite, además de limit/cost/reasoning/tool_call:
  - variants: {<nivel>: {}} desde thinking.levels (shape objeto o array)
  - x-thinking: {levels, default} prescriptivo (metadata para el agente)
  - modalities: {input: [...], output: [...]} desde modality (lado izq/der de "->")
  - attachment: true cuando input tiene >1 modalidad
  - x-modality: string crudo (referencia)
  Campos no-nativos quedan namespaced x-* (el nivel modelo tolera extras;
  los nativos van con su schema exacto).
postcondition: >
  El fragmento /v1/client-config?client=opencode representa el thinking como
  variants nativo y las modalidades como modalities+attachment nativos, con
  el default prescriptivo en x-thinking; suite -race verde y servicio deployado.
verification:
  - Modelo con Meta.thinking = {"levels":["low","high"],"default":"high"} → variants {"low":{},"high":{}} y x-thinking.default == "high"
  - Modelo con thinking ["always"] → variants {"always":{}}
  - Modelo sin thinking → sin variants ni x-thinking
  - Modelo con modality "text+image+video->text" → modalities {input:[text,image,video], output:[text]} + attachment true
  - Modelo con modality "text->text" → sin modalities, sin attachment, con x-modality
  - Modelo sin modality → sin modalities/attachment/x-modality
  - go test ./internal/clientconfig/opencode -count=1 verde
  - go test ./... -race -count=1 verde (suite completa)
  - Servicio reiniciado y active; smoke del endpoint real contiene "variants"
```
