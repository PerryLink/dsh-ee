CREATE TABLE package_sources (
  id BIGINT NOT NULL AUTO_INCREMENT,
  name VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  protocol VARCHAR(8) NOT NULL,
  mode VARCHAR(8) NOT NULL,
  upstream_url VARCHAR(2048) NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY uk_package_source (protocol, name)
);

CREATE TABLE package_source_owners (
  source_id BIGINT NOT NULL,
  namespace VARBINARY(1024) NOT NULL,
  owner_id BIGINT NOT NULL,
  PRIMARY KEY (source_id, namespace)
);

CREATE TABLE package_source_revisions (
  source_id BIGINT NOT NULL,
  path VARBINARY(512) NOT NULL,
  parent VARBINARY(512) NOT NULL,
  published_at DATETIME(3) NOT NULL,
  PRIMARY KEY (source_id, path),
  KEY idx_package_revision_parent (source_id, parent, published_at)
);

CREATE TABLE package_source_files (
  source_id BIGINT NOT NULL,
  path VARBINARY(1024) NOT NULL,
  revision VARBINARY(512) NOT NULL,
  blob_key CHAR(128) NOT NULL,
  size BIGINT NOT NULL,
  uploader_id BIGINT NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (source_id, path),
  KEY idx_package_files_revision (source_id, revision, path)
);
