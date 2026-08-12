# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later

# Módulo local de Odoo para el epic 011-mofgw-odoo.
#
# 011-007-embedding-vector: override del campo ai.embedding.embedding_vector
# a Vector(size=384) (dimensión nativa de all-minilm que mofgw fuerza por
# cliente, feature 011-006) + redimensionado del schema vía post_init_hook
# (DROP INDEX ivfflat + ALTER TYPE vector(384)) porque Odoo NO migra la
# columna vector automáticamente.
#
# 011-008-odoo-provider (feature hermana, mismo módulo): agrega el registro
# del provider mofgw/Ollama. Comparten instalación (D4).
{
    'name': 'mofgw_ai',
    'version': '1.0',
    'category': 'Hidden',
    'summary': 'mofgw como proveedor de IA de Odoo (epic 011)',
    'description': """Alinea la dimensión del vector de embeddings de Odoo a la
que mofgw fuerza por cliente (all-minilm=384) y registra mofgw como provider
de IA. 011-007: override embedding_vector=Vector(size=384) + redimensionado
del schema en post_init_hook. 011-008: registro del provider.""",
    'depends': ['ai'],
    'data': [
        'views/res_config_settings_views.xml',
    ],
    # redimension_embedding_vector vive en mofgw_ai/__init__.py (Odoo lo
    # resuelve al importar el paquete). pre_init_hook ejecuta DROP INDEX +
    # ALTER TYPE vector(384) ANTES del schema sync (init_models), que recrea
    # el índice ivfflat. Odoo NO migra la columna vector automáticamente
    # (update_db_column compara solo udt_name). Para upgrades, el mismo SQL
    # corre en migrations/1.0/pre-migrate.py (también antes de init_models).
    'pre_init_hook': 'redimension_embedding_vector',
    'installable': True,
    'application': False,
    'auto_install': False,
}
