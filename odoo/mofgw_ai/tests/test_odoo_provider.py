# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later

# Tests de módulo Odoo para 011-008-odoo-provider (test-audit, bloques B1-B9).
#
# Verifican el contrato observable de registrar mofgw como provider de IA de
# Odoo 19 enterprise (módulo local mofgw_ai): estado de PROVIDERS, métodos
# parcheados de LLMApiService, override de ai.embedding.embedding_model y
# settings por instancia. NO dependen de estructura interna del core `ai`
# (solo del comportamiento público documentado en spec.md).
#
# RED: B1/B2/B8/B9 fallan por AssertionError (registro/override ausente);
# B3-B7 fallan al construir LLMApiService(provider='mofgw') porque el provider
# no está registrado ni parcheado (UserError de provider desconocido).
# B10 (E2E en staging, P11/C7) NO se escribe acá por decisión de test-audit.

import os
from unittest import mock

from odoo.exceptions import UserError
from odoo.tests import TransactionCase

from odoo.addons.ai.utils.llm_providers import (
    PROVIDERS,
    get_provider,
    get_provider_for_embedding_model,
)
from odoo.addons.ai.utils.llm_api_service import LLMApiService

# URL default D5 del spec (mofgw sirve /v1/responses y /v1/embeddings).
MOFGW_DEFAULT_URL = "http://127.0.0.1:3369/v1"

# Los 5 modelos mofgw (spec, P1).
MOFGW_LLMS = [
    "deepseek-v4-flash",
    "deepseek-v4-pro",
    "qwen3.7-plus",
    "glm-5.2",
    "kimi-k2.7-code",
]


class TestOdooProvider(TransactionCase):
    """Contrato observable del registro de mofgw como provider de IA de Odoo."""

    def setUp(self):
        super().setUp()
        self.params = self.env["ir.config_parameter"].sudo()

    # B1 (P1, P2): PROVIDERS contiene el Provider 'mofgw', get_provider*
    # resuelven, y _get_llm_model_selection() expone los 5 modelos mofgw.
    def test_provider_registered(self):
        mofgw = next((p for p in PROVIDERS if p.name == "mofgw"), None)
        self.assertIsNotNone(mofgw, "PROVIDERS no contiene el provider 'mofgw'")
        self.assertEqual(mofgw.display_name, "mofgw")
        self.assertEqual(mofgw.embedding_model, "all-minilm")
        llm_names = [name for name, _ in mofgw.llms]
        self.assertEqual(llm_names, MOFGW_LLMS)

        # Resolución observable por llm_model y por embedding_model.
        self.assertEqual(get_provider(self.env, "deepseek-v4-flash"), "mofgw")
        self.assertEqual(
            get_provider_for_embedding_model(self.env, "all-minilm"), "mofgw"
        )

        # P2: el Selection de ai.agent.llm_model incluye los 5 modelos mofgw.
        selection = self.env["ai.agent"]._get_llm_model_selection()
        selection_names = [name for name, _ in selection]
        for model in llm_names:
            self.assertIn(model, selection_names)

    # B2 (P3, I4): un agente mofgw resuelve su modelo de embeddings a
    # 'all-minilm' (384-dim, consistente con la columna vector(384) de 011-007).
    def test_get_embedding_model_mofgw(self):
        agent = self.env["ai.agent"].new(
            {
                "name": "Mofgw Agent",
                "llm_model": "deepseek-v4-flash",
                "response_style": "analytical",
            }
        )
        self.assertEqual(agent._get_embedding_model(), "all-minilm")

    # B3 (P4): __init__ parcheado resuelve base_url para 'mofgw' (config o
    # default D5); openai conserva su base_url original (I2).
    def test_init_base_url(self):
        self.params.set_param("ai.mofgw_url", False)  # estado limpio
        service = LLMApiService(self.env, provider="mofgw")
        self.assertEqual(service.base_url, MOFGW_DEFAULT_URL)

        # Con ai.mofgw_url seteado usa ese valor.
        self.params.set_param("ai.mofgw_url", "http://custom.mofgw/v1")
        service2 = LLMApiService(self.env, provider="mofgw")
        self.assertEqual(service2.base_url, "http://custom.mofgw/v1")

        # I2: openai conserva su base_url original (no se ve afectado).
        openai = LLMApiService(self.env, provider="openai")
        self.assertNotEqual(openai.base_url, MOFGW_DEFAULT_URL)

    # B4 (P5): _get_api_token para 'mofgw' resuelve config -> env -> UserError;
    # openai conserva su token original (I2).
    def test_get_api_token(self):
        original_env_token = os.environ.get("ODOO_AI_MOFGW_TOKEN")
        try:
            # 1) config seteado -> devuelve ese valor.
            self.params.set_param("ai.mofgw_key", "cfg-key")
            service = LLMApiService(self.env, provider="mofgw")
            self.assertEqual(service._get_api_token(), "cfg-key")

            # 2) config ausente + env var -> devuelve el env var.
            self.params.set_param("ai.mofgw_key", False)  # borra
            os.environ["ODOO_AI_MOFGW_TOKEN"] = "env-key"
            service = LLMApiService(self.env, provider="mofgw")
            self.assertEqual(service._get_api_token(), "env-key")

            # 3) ninguno -> UserError con el mensaje del spec.
            os.environ.pop("ODOO_AI_MOFGW_TOKEN", None)
            service = LLMApiService(self.env, provider="mofgw")
            with self.assertRaises(UserError) as ctx:
                service._get_api_token()
            self.assertIn("No API key set for provider 'mofgw'", str(ctx.exception))

            # 4) I2: openai conserva su token original.
            self.params.set_param("ai.openai_key", "openai-key")
            openai = LLMApiService(self.env, provider="openai")
            self.assertEqual(openai._get_api_token(), "openai-key")
        finally:
            # Restaurar el env var (os.environ es global, no transaccional).
            if original_env_token is None:
                os.environ.pop("ODOO_AI_MOFGW_TOKEN", None)
            else:
                os.environ["ODOO_AI_MOFGW_TOKEN"] = original_env_token

    # B5 (P6): _request_llm('mofgw') delega a _request_llm_openai; openai
    # conserva su despacho original (I2, I3 — ambos por el path OpenAI).
    @mock.patch(
        "odoo.addons.ai.utils.llm_api_service.LLMApiService._request_llm_openai"
    )
    def test_request_llm_delegates(self, mock_openai):
        mock_openai.return_value = [{"role": "assistant", "content": "ok"}]
        system = [{"role": "system", "content": "You are helpful."}]
        user = [{"role": "user", "content": "Hello"}]

        mofgw = LLMApiService(self.env, provider="mofgw")
        res = mofgw._request_llm("deepseek-v4-flash", system, user)
        mock_openai.assert_called_once()
        self.assertEqual(res, [{"role": "assistant", "content": "ok"}])

        # I2/I3: openai conserva su despacho original (sigue pasando por
        # _request_llm_openai, sin regresión).
        openai = LLMApiService(self.env, provider="openai")
        openai_res = openai._request_llm("gpt-4.1", system, user)
        self.assertEqual(openai_res, [{"role": "assistant", "content": "ok"}])
        self.assertEqual(mock_openai.call_count, 2)

    # B6 (P7): _build_tool_call_response('mofgw') produce formato openai;
    # openai conserva su formato original (I2).
    def test_build_tool_call_response(self):
        """P7 — _build_tool_call_response para 'mofgw' produce exactamente el
        formato openai (function_call_output). El core openai (llm_api_service.py:678-683)
        ya devuelve {"type":"function_call_output","call_id":id,"output":str(val)};
        el parche de mofgw replica ese mismo dict, por lo que AMBOS formatos son
        iguales. Verifica el contrato observable (I2, I3): mofgw == openai."""
        tool_call_id = 'call_123'
        return_value = {'key': 'value'}

        # core openai como oráculo independiente del formato esperado
        openai_service = LLMApiService(self.env, provider='openai')
        openai_result = openai_service._build_tool_call_response(
            tool_call_id, return_value)

        mofgw_service = LLMApiService(self.env, provider='mofgw')
        result = mofgw_service._build_tool_call_response(
            tool_call_id, return_value)

        # FIX: mofgw DEBE producir exactamente el formato openai (no uno distinto)
        self.assertEqual(result, openai_result)
        self.assertEqual(
            result,
            {"type": "function_call_output", "call_id": tool_call_id,
             "output": str(return_value)},
        )

    # B7 (P8): get_embedding de mofgw sale por POST <base_url>/embeddings con
    # Authorization Bearer ai.mofgw_key, usando base_url + token parcheados.
    @mock.patch("odoo.addons.ai.utils.llm_api_service.LLMApiService._request")
    def test_get_embedding(self, mock_request):
        """P8/C7 — get_embedding devuelve el EmbeddingResponse COMPLETO
        (dict {object,data,model,usage}), NO el vector directo. El vector se
        extrae de response['data'][0]['embedding']."""
        self.params.set_param("ai.mofgw_url", "http://mofgw.test/v1")
        self.params.set_param("ai.mofgw_key", "mofgw-test-key")

        calls = []

        def handler(method, endpoint, headers, body, params=None, **kwargs):
            calls.append(
                {"method": method, "endpoint": endpoint, "headers": headers,
                 "body": body}
            )
            return {
                "data": [{"embedding": [0.1] * 384, "index": 0,
                          "object": "embedding"}],
                "model": "all-minilm",
            }

        mock_request.side_effect = handler
        service = LLMApiService(self.env, provider="mofgw")

        response = service.get_embedding("hello world", dimensions=384,
                                         model="all-minilm")

        # FIX: extraer el vector de la estructura EmbeddingResponse
        vector = response["data"][0]["embedding"]
        self.assertEqual(len(vector), 384)

        self.assertEqual(len(calls), 1)
        # requests normaliza el método a minúsculas al llamar _request.
        self.assertEqual(calls[0]["method"], "post")
        self.assertEqual(calls[0]["endpoint"], "/embeddings")
        self.assertEqual(
            calls[0]["headers"]["Authorization"], "Bearer mofgw-test-key"
        )
        self.assertEqual(calls[0]["body"]["model"], "all-minilm")

    # B8 (P9): res.config.settings hereda mofgw_url/mofgw_key con
    # config_parameter; guardar setea ir.config_parameter; view heredada existe.
    def test_settings_config(self):
        settings_model = self.env["res.config.settings"]

        # P9: expone los fields.
        self.assertIn("mofgw_url", settings_model._fields)
        self.assertIn("mofgw_key", settings_model._fields)

        # Guardar en settings setea ir.config_parameter.
        settings = settings_model.create({})
        settings.mofgw_url = "http://settings/v1"
        settings.mofgw_key = "settings-key"
        settings.execute()
        self.assertEqual(self.params.get_param("ai.mofgw_url"), "http://settings/v1")
        self.assertEqual(self.params.get_param("ai.mofgw_key"), "settings-key")

        # View heredada de res_config_settings_view_form que expone los fields.
        base_view = self.env.ref("base.res_config_settings_view_form")
        inherited = self.env["ir.ui.view"].search(
            [("inherit_id", "=", base_view.id)]
        )
        found = inherited.filtered(
            lambda v: "mofgw" in (v.name or "").lower()
            or "mofgw" in (v.arch or "").lower()
        )
        self.assertTrue(found, "no hay view heredada que exponga fields mofgw")

    # B9 (P10): ai.embedding.embedding_model incluye ('all-minilm',
    # 'All-minilm'), conservando required=True y los valores del core (I5).
    def test_embedding_model_selection(self):
        """P10 — ai.embedding.embedding_model incluye 'all-minilm' conservando
        required=True y los valores del core. NOTA: get_values() de un Selection
        (fields_selection.py:219-224) devuelve una LISTA de strings (los valores),
        NO tuples; los tuples (value, label) se leen via field.selection."""
        field = self.env["ai.embedding"]._fields["embedding_model"]

        # FIX: get_values() devuelve strings, no tuples
        self.assertIn("all-minilm", field.get_values(self.env))
        # el label 'All-minilm' se verifica via field.selection (que sí es tuple)
        self.assertIn(("all-minilm", "All-minilm"), field.selection)

        # conserva los valores del core (openai/google) y required=True
        self.assertIn("text-embedding-3-small", field.get_values(self.env))
        self.assertIn("gemini-embedding-001", field.get_values(self.env))
        self.assertTrue(field.required)
