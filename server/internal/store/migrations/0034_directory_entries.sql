-- 企业扩展目录 v1（docs/plugin-signing.md §2.2、roadmap-plugin-distribution.md §3）：
-- 管理员把一份签过名的 dsh-ee-directory.json 贴进管理接口，验签 + 逐条校验后登记到这里。
-- 只登记「来源」，不自动安装、不自动升级；apply 是管理员动作，同一 directory_id 覆盖登记
-- （目录里删掉/改掉的条目在 apply 时同步），不同目录互不影响。
--
-- 条目里存的是目录声明，不是安装结果：真正装扩展/插件仍走现有安装流程（含扩展验签与审核）。

CREATE TABLE directory_entries (
  id                BIGINT        NOT NULL AUTO_INCREMENT,
  -- 企业目录标识（format 2 的 directory_id）；一个企业可以有多份目录
  directory_id      VARCHAR(64)   NOT NULL,
  -- dsh-plugin（npm 包）或 extension（服务端扩展）
  kind              VARCHAR(16)   NOT NULL,
  -- 扩展是「作者/名字」，插件是 npm 包名
  name              VARCHAR(214)  NOT NULL,
  version           VARCHAR(64)   NOT NULL,
  -- stable / beta（插件对应 dist-tag latest / beta）
  channel           VARCHAR(16)   NOT NULL,
  -- 扩展：清单（声明式连同接口文档）的 sha256 十六进制；插件：npm 的 sha512 SRI
  digest            VARCHAR(160)  NOT NULL,
  source_type       VARCHAR(16)   NOT NULL,
  -- npm 来源可选的 registry 地址（https）
  source_registry   VARCHAR(512)  NOT NULL DEFAULT '',
  -- tarball 来源的固定地址（https）
  source_url        VARCHAR(2048) NOT NULL DEFAULT '',
  -- 条目声明的 tarball 大小（字节），0 表示没声明
  size              BIGINT        NOT NULL DEFAULT 0,
  publisher_user_id BIGINT        NULL,
  publisher_username   VARCHAR(128) NOT NULL DEFAULT '',
  publisher_department VARCHAR(128) NOT NULL DEFAULT '',
  notes             VARCHAR(1000) NOT NULL DEFAULT '',
  -- 条目内嵌签名的声明（目录里没有内容，只核对声明；安装时再验）
  signature_key_id  VARCHAR(64)   NOT NULL DEFAULT '',
  signature_key_state VARCHAR(16) NOT NULL DEFAULT '',
  signature_json    TEXT          NOT NULL,
  -- 这份条目所属目录文档的摘要：目录整体签名覆盖的 canonical payload 的 sha256
  document_digest   CHAR(64)      NOT NULL,
  applied_by        BIGINT        NULL,
  applied_at        DATETIME(3)   NOT NULL,
  created_at        DATETIME(3)   NOT NULL,
  updated_at        DATETIME(3)   NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_directory_entries_identity (directory_id, kind, name, version, channel),
  KEY idx_directory_entries_directory (directory_id, kind, name)
);
