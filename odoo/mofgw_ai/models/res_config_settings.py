# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later

# 011-008-odoo-provider (D4): settings por instancia para configurar mofgw.
# Mismo patrón que ai.openai_key / ai.google_key del core (res_config_settings.py).
# ai.mofgw_url + ai.mofgw_key son ir.config_parameter globales (I6).

from odoo import fields, models


class ResConfigSettings(models.TransientModel):
    _inherit = 'res.config.settings'

    mofgw_url = fields.Char(
        string="mofgw URL",
        config_parameter='ai.mofgw_url',
    )
    mofgw_key = fields.Char(
        string="mofgw API key",
        config_parameter='ai.mofgw_key',
    )
