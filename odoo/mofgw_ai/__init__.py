# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later
import os

from odoo import _

from odoo.exceptions import UserError

from odoo.addons.ai.utils.llm_api_service import LLMApiService
from odoo.addons.ai.utils.llm_providers import PROVIDERS, Provider

from . import models


# ============================================================================
# 011-007-embedding-vector: redimensiona ai.embedding.embedding_vector a
# vector(384). pre_init_hook del manifest (corre ANTES del schema sync, así
# init_models recrea el índice ivfflat). Odoo 19 lo invoca con UN arg (env).
# ============================================================================
def redimension_embedding_vector(env):
    """Redimensiona ai.embedding.embedding_vector a vector(384).

    Odoo no altera la columna vector por el override del field (no-op por
    udt_name), y el índice ivfflat exige DROP antes del ALTER. El hook corre en
    instalación (pre_init_hook, antes del schema sync) y garantiza que el
    índice se recrea tras el ALTER. Idempotente: skip si ya está en 384."""
    cr = env.cr
    cr.execute(
        "SELECT format_type(atttypid, atttypmod) FROM pg_attribute "
        "WHERE attrelid = 'ai_embedding'::regclass AND attname = 'embedding_vector'")
    row = cr.fetchone()
    if row and row[0] == 'vector(384)':
        return  # ya en la dimensión correcta — idempotente
    cr.execute("DROP INDEX IF EXISTS ai_embedding_embedding_vector_idx")
    cr.execute(
        "ALTER TABLE ai_embedding ALTER COLUMN embedding_vector TYPE vector(384)")


# ============================================================================
# 011-008-odoo-provider: registro de mofgw como provider de IA de Odoo.
# ============================================================================
# Monkeypatch in-place (D1 del spec): el core de `ai` instancia LLMApiService
# por nombre y comparte PROVIDERS por referencia. Este módulo hace
# PROVIDERS.append + parchea los métodos de la clase base para el caso
# provider='mofgw', delegando al original para openai/google (sin regresión).

MOFGW_DEFAULT_URL = "http://127.0.0.1:3369/v1"

MOFGW_LLMS = [
    ("deepseek-v4-flash", "DeepSeek V4 Flash"),
    ("deepseek-v4-pro", "DeepSeek V4 Pro"),
    ("qwen3.7-plus", "Qwen 3.7 Plus"),
    ("glm-5.2", "GLM 5.2"),
    ("kimi-k2.7-code", "Kimi K2.7 Code"),
]


def _register_mofgw_provider():
    """Registra mofgw en PROVIDERS (idempotente ante recarga del módulo)."""
    if not any(p.name == "mofgw" for p in PROVIDERS):
        PROVIDERS.append(Provider(
            "mofgw", "mofgw", "all-minilm", MOFGW_LLMS,
        ))


def _patch_llm_api_service():
    """Parchea in-place los métodos de LLMApiService para provider='mofgw'.

    Cada parche captura el método original y delega para openai/google (I2).
    Idempotente: evita re-aplicar si el módulo se recarga."""
    # Marcamos el parche como aplicado para idempotencia.
    if getattr(LLMApiService, "_mofgw_patched", False):
        return
    LLMApiService._mofgw_patched = True

    _orig_init = LLMApiService.__init__

    def _patched_init(self, env, provider='openai'):
        if provider == 'mofgw':
            self.provider = provider
            self.base_url = env["ir.config_parameter"].sudo().get_param(
                "ai.mofgw_url", MOFGW_DEFAULT_URL)
            self.env = env
        else:
            _orig_init(self, env, provider=provider)

    LLMApiService.__init__ = _patched_init

    _orig_token = LLMApiService._get_api_token

    def _patched_get_api_token(self):
        if self.provider == 'mofgw':
            cfg = {"config_key": "ai.mofgw_key", "env_var": "ODOO_AI_MOFGW_TOKEN"}
            if key := (self.env["ir.config_parameter"].sudo().get_param(
                    cfg["config_key"]) or os.getenv(cfg["env_var"])):
                return key
            raise UserError(_("No API key set for provider 'mofgw'"))
        return _orig_token(self)

    LLMApiService._get_api_token = _patched_get_api_token

    _orig_llm = LLMApiService._request_llm

    def _patched_request_llm(self, *args, **kwargs):
        if self.provider == 'mofgw':
            return self._request_llm_openai(*args, **kwargs)
        return _orig_llm(self, *args, **kwargs)

    LLMApiService._request_llm = _patched_request_llm

    _orig_build = LLMApiService._build_tool_call_response

    def _patched_build_tool_call_response(self, tool_call_id, return_value):
        if self.provider == 'mofgw':
            return {"type": "function_call_output", "call_id": tool_call_id,
                    "output": str(return_value)}
        return _orig_build(self, tool_call_id, return_value)

    LLMApiService._build_tool_call_response = _patched_build_tool_call_response


_register_mofgw_provider()
_patch_llm_api_service()
