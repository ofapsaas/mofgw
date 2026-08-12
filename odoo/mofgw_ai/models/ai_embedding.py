# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later

# 011-007-embedding-vector: override del campo embedding_vector a la dimensión
# que mofgw fuerza por cliente (all-minilm = 384, feature 011-006).
#
# Por qué: mofgw (011-006) fuerza el modelo de embeddings por cliente y
# devuelve la dimensión NATIVA del modelo (all-minilm = 384). Odoo tenía
# Vector(size=1536) hardcodeado. El vector real que recibe Odoo es 384, y
# Postgres exige que la columna vector(384) coincida (el operador <=> falla si
# las dims difieren). El size se declara AQUI (override), fijo por instancia
# (config manual según plan-011).
#
# _get_dimensions() (ai_embedding.py:37 del core) lee el size en runtime →
# devuelve 384 automáticamente, sin tocar el core (I2/P2).

from odoo import fields, models

from odoo.addons.ai.orm.field_vector import Vector


class AIEmbedding(models.Model):
    _inherit = 'ai.embedding'

    # Dimensión nativa de all-minilm que mofgw fuerza por cliente (011-006).
    embedding_vector = Vector(size=384)

    # 011-008-odoo-provider (D3): EMBEDDING_MODELS_SELECTION es un snapshot
    # separado calculado al import de llm_providers.py — PROVIDERS.append NO lo
    # actualiza. El valor 'all-minilm' debe estar en la Selection del campo
    # ai.embedding.embedding_model para que el RAG/cron puedan escribir
    # embeddings 384-dim (consistente con la columna vector(384)).
    # ondelete='cascade' requerido por Odoo para selection_add con required
    # (política de limpieza al desinstalar el módulo).
    embedding_model = fields.Selection(
        selection_add=[('all-minilm', 'All-minilm')],
        ondelete={'all-minilm': 'cascade'},
    )
