-- Plan catalogue and the data-source catalogue.
--
-- Prices here are the *defaults shipped with the release*; they are ordinary
-- data rows, so operations can change them with an UPDATE (or env-driven seed)
-- without touching business logic. Money is NUMERIC, never a float.

INSERT INTO plans (code, name, description, max_channels, history_days, max_members,
                   max_campaigns, features, price_monthly, currency, trial_days, sort_order)
VALUES
    ('free', 'FREE', 'Знакомство с продуктом: базовая аналитика и до 3 каналов.',
     3, 7, 1, 3,
     '{"comparison": false, "csv_export": false, "campaigns": true, "api_access": false,
       "advanced_analytics": false, "notifications": true, "monitoring_interval_minutes": 720}'::jsonb,
     0.00, 'RUB', 0, 10),

    ('pro', 'PRO', 'Для владельцев каналов и рекламодателей: сравнение, кампании, отчёты.',
     30, 90, 3, 50,
     '{"comparison": true, "csv_export": true, "campaigns": true, "api_access": false,
       "advanced_analytics": false, "notifications": true, "monitoring_interval_minutes": 180}'::jsonb,
     1490.00, 'RUB', 7, 20),

    ('business', 'BUSINESS', 'Для агентств: расширенная аналитика, команда, приоритетный мониторинг.',
     150, 365, 5, 500,
     '{"comparison": true, "csv_export": true, "campaigns": true, "api_access": true,
       "advanced_analytics": true, "notifications": true, "monitoring_interval_minutes": 60}'::jsonb,
     4990.00, 'RUB', 7, 30)
ON CONFLICT (code) DO NOTHING;

INSERT INTO data_sources (code, name, kind, reliability, description, is_enabled, rate_limit_minutes, config)
VALUES
    ('telegram_bot_api', 'Telegram Bot API',
     'telegram', 'verified',
     'Официальный Bot API. Даёт достоверные данные только для каналов, где бот администратор (собственный канал пользователя). Для чужих каналов статистика недоступна.',
     TRUE, 60, '{"requires": "bot_is_channel_admin", "metrics": ["subscriber_count", "post_views"], "limitation": "no_statistics_for_foreign_channels"}'::jsonb),

    ('provider_api', 'Внешний аналитический провайдер',
     'api', 'estimated',
     'Подключаемый адаптер внешнего аналитического API (например, TGStat, Telemetr или собственный сервис). Показатели помечаются как estimated до верификации; ключ API задаётся через переменные окружения.',
     FALSE, 120, '{"env": ["PROVIDER_API_BASE_URL", "PROVIDER_API_KEY"], "metrics": ["subscriber_count", "views", "average_views", "err"]}'::jsonb),

    ('csv_import', 'Импорт CSV/XLSX',
     'import', 'unverified',
     'Загрузка выгрузок статистики. Значения считаются предоставленными пользователем (unverified) до подтверждения провайдером.',
     TRUE, 0, '{"max_file_bytes": 10485760, "formats": ["csv", "xlsx"]}'::jsonb),

    ('manual_entry', 'Ручной ввод',
     'manual', 'unverified',
     'Показатели, введённые пользователем вручную (например, по скриншоту статистики канала).',
     TRUE, 0, '{}'::jsonb)
ON CONFLICT (code) DO NOTHING;
