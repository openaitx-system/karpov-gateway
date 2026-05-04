-- +goose Up
SET search_path TO quota;

-- 历史脏数据清理：v0.5.x 之前的录制 middleware 缺少 provider 白名单，
-- 路径首段是任意字符串都会被记成 provider，导致 `usage_aggregates_*`
-- 出现 provider IN ('v1','api','unknown','') 这类垃圾行。
-- v0.5.x 起 server.go 的 UsageRecorder 中间件强制按 KnownProviders 过滤，
-- 这里把已经入库的脏行一次性删干净。
--
-- 注意：保留 `unknown` 作为兜底"未识别但来自合法 provider 路径"的占位是
-- 没必要的——白名单生效后这条永远不会再写入；删了不会影响正确数据。
DELETE FROM usage_aggregates_daily
 WHERE provider IN ('', 'v1', 'api', 'unknown');

DELETE FROM usage_aggregates_monthly
 WHERE provider IN ('', 'v1', 'api', 'unknown');

-- +goose Down
SET search_path TO quota;

-- 清理操作不可回滚（脏数据本身没有保留价值）。
SELECT 1;
