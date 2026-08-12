# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later

# Tests de módulo Odoo para 011-007-embedding-vector (test-audit, bloques B1-B7).
#
# Verifican el contrato observable: override del field (size==384),
# _get_dimensions()==384, y el schema real en PostgreSQL (columna vector(384),
# índice ivfflat recreado). NO dependen de estructura interna del core ai.

from odoo.tests import TransactionCase


class TestEmbeddingVector(TransactionCase):
    def setUp(self):
        super().setUp()
        self.AIEmbedding = self.env['ai.embedding']

    # B1 (P1 + P2): dimensión declarada y _get_dimensions() == 384.
    def test_get_dimensions_and_field_size(self):
        self.assertEqual(self.AIEmbedding._get_dimensions(), 384)
        self.assertEqual(
            self.AIEmbedding._fields['embedding_vector'].size, 384)

    # B4 (P6 + P8a): la columna en la DB quedó en vector(384).
    def test_column_vector384(self):
        self.env.cr.execute(
            "SELECT format_type(atttypid, atttypmod) FROM pg_attribute "
            "WHERE attrelid = 'ai_embedding'::regclass "
            "AND attname = 'embedding_vector'")
        row = self.env.cr.fetchone()
        self.assertTrue(row, "columna embedding_vector no encontrada")
        self.assertEqual(row[0], 'vector(384)')

    # B5 (P7 + P8b + I4): el índice ivfflat se recreó tras la migración.
    def test_index_recreated(self):
        self.env.cr.execute(
            "SELECT indexdef FROM pg_indexes "
            "WHERE indexname = 'ai_embedding_embedding_vector_idx'")
        row = self.env.cr.fetchone()
        self.assertTrue(row, "índice ivfflat no recreado tras la migración")
        self.assertIn("ivfflat", row[0])
        self.assertIn("embedding_vector vector_cosine_ops", row[0])

    # B6 (P9 + C4): el redimensionado es idempotente — re-ejecutar el hook en
    # una instancia que ya tiene la columna en vector(384) no lanza error y
    # deja el schema intacto.
    def test_redimension_idempotente(self):
        # simular la llamada del hook en un estado ya redimensionado
        from odoo.addons.mofgw_ai import redimension_embedding_vector
        redimension_embedding_vector(self.env)
        # verificar que la columna sigue en 384 y el índice existe
        self.test_column_vector384()
        self.test_index_recreated()
