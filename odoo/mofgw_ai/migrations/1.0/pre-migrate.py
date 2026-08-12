# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later

# Migración pre de mofgw_ai para UPGRADES (corre en etapa 'pre',
# loading.py:166, ANTES de init_models → el índice ivfflat se recrea tras el
# ALTER). Idéntica lógica al pre_init_hook (install); pre-migrate cubre el
# caso upgrade, donde post_init_hook NO corre (loading.py:231-238).
#
# Idempotente: skip si la columna ya está en vector(384).

import logging

_logger = logging.getLogger(__name__)


def pre_migrate(cr, version):
    cr.execute(
        "SELECT format_type(atttypid, atttypmod) FROM pg_attribute "
        "WHERE attrelid = 'ai_embedding'::regclass AND attname = 'embedding_vector'")
    row = cr.fetchone()
    if row and row[0] == 'vector(384)':
        _logger.info("mofgw_ai pre_migrate: ai_embedding.embedding_vector ya en vector(384), skip")
        return
    _logger.info(
        "mofgw_ai pre_migrate: redimensionando ai_embedding.embedding_vector a vector(384)")
    cr.execute("DROP INDEX IF EXISTS ai_embedding_embedding_vector_idx")
    cr.execute("ALTER TABLE ai_embedding ALTER COLUMN embedding_vector TYPE vector(384)")
