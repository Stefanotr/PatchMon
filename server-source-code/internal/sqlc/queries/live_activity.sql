-- name: GetOpenAgentUpdate :one
-- The agent update of a host still open (active, or waiting for the agent),
-- the latest one sent after created_after.
SELECT job_id, status, output, created_at
FROM job_history
WHERE api_id = sqlc.arg('api_id')::text
  AND queue_name = sqlc.arg('queue_name')::text
  AND job_name = sqlc.arg('job_name')::text
  AND status IN ('active', 'waiting')
  AND created_at > sqlc.arg('created_after')::timestamp
ORDER BY created_at DESC
LIMIT 1;

-- name: RecordAgentUpdateStage :execrows
-- Records a stage of an agent update on its row: the output keys in patch
-- are merged, and stages.<stage> is set to at unless already set (when the
-- stage began). Only an open row of that agent changes; status ends it when
-- completed or failed.
UPDATE job_history
SET output = COALESCE(output, '{}'::jsonb)
        || COALESCE(sqlc.narg('patch')::jsonb, '{}'::jsonb)
        || jsonb_build_object('stages',
               jsonb_build_object(sqlc.arg('stage')::text, sqlc.arg('at')::text) || COALESCE(output->'stages', '{}'::jsonb)),
    status = sqlc.arg('status')::text,
    error_message = sqlc.narg('error_message')::text,
    completed_at = CASE WHEN sqlc.arg('status')::text IN ('completed', 'failed') THEN NOW() ELSE completed_at END,
    updated_at = NOW()
WHERE job_id = sqlc.arg('job_id')::text
  AND api_id = sqlc.arg('api_id')::text
  AND job_name = sqlc.arg('job_name')::text
  AND status IN ('active', 'waiting')
  AND sqlc.arg('status')::text IN ('active', 'completed', 'failed');

-- name: ListLiveJobHistory :many
-- What a host's agent is doing now: jobs still open that started after
-- open_after, and jobs that ended after finished_after (shown for a moment
-- with their outcome).
SELECT id, job_id, queue_name, job_name, status, attempt_number, error_message, output, created_at, updated_at, completed_at
FROM job_history
WHERE api_id = sqlc.arg('api_id')::text
  AND ((status IN ('active', 'waiting', 'delayed') AND created_at > sqlc.arg('open_after')::timestamp)
    OR (status IN ('completed', 'failed') AND completed_at > sqlc.arg('finished_after')::timestamp))
ORDER BY created_at DESC
LIMIT sqlc.arg('max_rows')::int;

-- name: ListLivePatchRuns :many
-- A host's patch runs queued or running, and those that ended after
-- finished_after, with the end of their output.
SELECT id, patch_type, package_name, status, dry_run, error_message, started_at, completed_at, scheduled_at, created_at,
       RIGHT(shell_output, 2000)::text AS output_tail
FROM patch_runs
WHERE host_id = sqlc.arg('host_id')::text
  AND (status IN ('queued', 'running')
    OR (status NOT IN ('queued', 'running') AND completed_at > sqlc.arg('finished_after')::timestamp))
ORDER BY created_at DESC
LIMIT 10;
