-- 一个账号可以同时带飞书和钉钉两个身份。users.external_dn 仍是账号的主身份（建号时的那个），
-- 这张表记每个身份和目录里核实过的联系方式摘要（手机号 / 企业邮箱的 SHA-256，不存明文）。
-- via：home 是主身份的镜像（以 users.external_dn 为准）；explicit 是员工自己绑定的；
-- mobile / email 是按核实过的联系方式自动关联的。
CREATE TABLE user_identities (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 user_id BIGINT NOT NULL,
 provider VARCHAR(16) NOT NULL,
 subject VARBINARY(512) NOT NULL,
 via VARCHAR(16) NOT NULL,
 mobile_hash CHAR(64) NOT NULL DEFAULT '',
 email_hash CHAR(64) NOT NULL DEFAULT '',
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 UNIQUE KEY uq_identity_subject (subject),
 UNIQUE KEY uq_identity_user_provider (user_id, provider),
 KEY idx_identity_mobile (mobile_hash),
 KEY idx_identity_email (email_hash)
);
