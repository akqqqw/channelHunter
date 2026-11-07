-- Seed rows are referenced by subscriptions/data_sources; drop only if unused.
DELETE FROM data_sources WHERE code IN ('telegram_bot_api', 'provider_api', 'csv_import', 'manual_entry');
DELETE FROM plans WHERE code IN ('free', 'pro', 'business');
