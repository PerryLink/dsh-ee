ALTER TABLE user_identities
 ADD COLUMN active_provider VARCHAR(16) GENERATED ALWAYS AS (IF(via = 'unlinked', NULL, provider)) VIRTUAL;

ALTER TABLE user_identities ADD UNIQUE KEY uq_identity_user_active_provider (user_id, active_provider);

ALTER TABLE user_identities DROP INDEX uq_identity_user_provider;
