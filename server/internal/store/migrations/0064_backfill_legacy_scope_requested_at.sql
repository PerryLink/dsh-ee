-- 升级回填：给升级前遗留的「扩大可用范围」申请补上快照标识 scope_requested_at。
-- 审批接口现在要求原样带回这份申请的 scope_requested_at（见 ResolveAssetScope 的快照校验）；
-- 升级前提交、还没被审批的申请如果该列为 NULL，网页 / 飞书 / Agent 都拿不到令牌，
-- 只会一直拿到 400 / 409，负责人只能重新提交一遍。
-- 申请写入时 updated_at 和 scope_requested_at 是同一个时间点（RequestAssetScope 一条 UPDATE 同时写），
-- 所以用 updated_at 回填就是当时那份申请的标识；两列都是 DATETIME(3)，精度一致。
-- pending_scope 为空串的行按「没有在审申请」处理（与 scanAsset 一致），不回填。
-- 只改 scope_requested_at 仍为 NULL 的行：迁移未跑完、或回填后新提交的申请都不会被覆盖，
-- 重复执行也安全。
UPDATE assets
SET scope_requested_at = updated_at
WHERE pending_scope IS NOT NULL
  AND pending_scope <> ''
  AND scope_requested_at IS NULL;
