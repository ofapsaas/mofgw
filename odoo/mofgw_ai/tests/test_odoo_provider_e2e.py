# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later

# E2E de 011-008-odoo-provider (B10, P11/C7): llamada REAL contra mofgw.
# Requiere: ai.mofgw_url apuntando a un mofgw alcanzable + ai.mofgw_key válida.
# En el staging del VPS, ai.mofgw_url=http://127.0.0.1:3369/v1 (túnel reverso
# a mofgw local) y ai.mofgw_key=<test-key>. Este test hace llamadas
# request_llm y get_embedding reales contra mofgw (no mock).

from odoo.tests import TransactionCase

from odoo.addons.ai.utils.llm_api_service import LLMApiService


class TestOdooProviderE2E(TransactionCase):
    """Verificación end-to-end contra mofgw real (P11/C7)."""

    def test_request_llm_e2e(self):
        svc = LLMApiService(self.env, provider="mofgw")
        res = svc.request_llm(
            "deepseek-v4-flash",
            ["You are a helpful assistant."],
            ["Respond with exactly: PONG"],
        )
        self.assertTrue(res, "request_llm no devolvió respuesta")
        joined = " ".join(res)
        self.assertIn("PONG", joined.upper(),
                      "respuesta no contiene PONG: %r" % res)

    def test_get_embedding_e2e(self):
        """P8/C7 E2E: get_embedding real contra mofgw → Ollama (all-minilm 384)."""
        svc = LLMApiService(self.env, provider="mofgw")
        resp = svc.get_embedding("hola mundo", dimensions=384, model="all-minilm")
        # resp es el EmbeddingResponse completo; data[0].embedding es el vector.
        vector = resp["data"][0]["embedding"]
        self.assertEqual(len(vector), 384,
                         "vector no es 384-dim: %d" % len(vector))

    def test_server_actions_use_mofgw(self):
        """D6: ir.actions.server overridea AI_PROVIDER/AI_MODEL a mofgw
        (server actions state='ai' ya no usan openai/gpt-4.1)."""
        server = self.env["ir.actions.server"]
        self.assertEqual(server.AI_PROVIDER, "mofgw",
                         "AI_PROVIDER no overrideado a mofgw (sigue OpenAI)")
        self.assertEqual(server.AI_MODEL, "deepseek-v4-flash",
                         "AI_MODEL no overrideado a deepseek-v4-flash")


