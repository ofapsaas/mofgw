# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later

# 011-008-odoo-provider (D6, resuelto): override de los atributos de clase
# AI_PROVIDER/AI_MODEL de ir.actions.server para que las SERVER ACTIONS
# (state='ai') usen mofgw en vez de OpenAI (openai/gpt-4.1 hardcodeado en el
# core enterprise/ai/models/ir_actions_server.py:26-27).
#
# El core instancia `LLMApiService(provider=self.AI_PROVIDER)` y llama
# `request_llm(self.AI_MODEL, ...)` (ir_actions_server.py:276-277). Como
# `AI_PROVIDER`/`AI_MODEL` son atributos de clase, re-declararlos en un modelo
# heredado (override declarativo de Odoo, NO monkeypatch) hace que las server
# actions usen el provider 'mofgw' registrado (011-008) con deepseek-v4-flash.
#
# mofgw (provider 'mofgw') reutiliza el flujo openai de LLMApiService
# (features 001-005): structured output, tool calling, etc. disponibles.

from odoo import models


class IrActionsServer(models.Model):
    _inherit = "ir.actions.server"

    # mofgw como provider de las server actions (reemplazo total de OpenAI).
    # 'mofgw' está registrado en PROVIDERS por mofgw_ai/__init__.py (011-008).
    AI_PROVIDER = "mofgw"
    AI_MODEL = "deepseek-v4-flash"
