-- name: ListJobHistoryByApiID :many
SELECT id, job_id, queue_name, job_name, host_id, api_id, status, attempt_number, error_message, output, created_at, updated_at, completed_at
FROM job_history
WHERE api_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: InsertJobHistory :exec
INSERT INTO job_history (id, job_id, queue_name, job_name, host_id, api_id, status, attempt_number, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW());

-- name: InsertJobHistoryOutcome :exec
INSERT INTO job_history (id, job_id, queue_name, job_name, host_id, api_id, status, attempt_number, error_message, output, created_at, updated_at, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, $9, NOW(), NOW(), NOW());

-- name: UpdateJobHistoryCompleted :exec
UPDATE job_history
SET status = 'completed', completed_at = NOW(), updated_at = NOW()
WHERE job_id = $1;

-- name: UpdateJobHistoryFailed :exec
UPDATE job_history
SET status = 'failed', error_message = $2, completed_at = NOW(), updated_at = NOW()
WHERE job_id = $1;

-- name: UpdateJobHistoryDelayed :exec
UPDATE job_history
SET status = 'delayed', updated_at = NOW()
WHERE job_id = $1;

-- name: InsertJobHistoryStarted :exec
-- A command sent outside the queue (a host restart), recorded before it is
-- sent; UpdateAgentCommandOutcome records the outcome on the same row.
INSERT INTO job_history (id, job_id, queue_name, job_name, host_id, api_id, status, attempt_number, output, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, 'active', 1, $7, NOW(), NOW());

-- name: UpdateAgentCommandOutcome :execrows
-- Records the outcome of a command InsertJobHistoryStarted recorded. Only the
-- host the command was sent to (api_id) can change its row, and output keys
-- are merged into the ones recorded with the command. The status guard is the
-- state machine: active -> completed, failed or waiting; waiting (a late
-- answer still possible) -> completed or failed; nothing leaves completed or
-- failed.
UPDATE job_history
SET status = sqlc.arg('status')::text,
    error_message = sqlc.narg('error_message')::text,
    output = COALESCE(output, '{}'::jsonb) || COALESCE(sqlc.narg('output')::jsonb, '{}'::jsonb),
    completed_at = CASE WHEN sqlc.arg('status')::text IN ('completed', 'failed') THEN NOW() ELSE NULL END,
    updated_at = NOW()
WHERE job_id = sqlc.arg('job_id')::text
  AND api_id = sqlc.arg('api_id')::text
  AND queue_name = sqlc.arg('queue_name')::text
  AND job_name = ANY(sqlc.arg('job_names')::text[])
  AND sqlc.arg('status')::text IN ('completed', 'failed', 'waiting')
  AND (status = 'active'
       OR (status = 'waiting' AND sqlc.arg('status')::text IN ('completed', 'failed')));

-- name: CloseStaleAgentCommands :execrows
-- Fails the commands still open (active, or waiting for a late answer) that
-- were sent before stale_before: no answer is coming any more (agent gone,
-- older agent, command lost).
UPDATE job_history
SET status = 'failed',
    error_message = sqlc.arg('error_message')::text,
    completed_at = NOW(),
    updated_at = NOW()
WHERE queue_name = sqlc.arg('queue_name')::text
  AND job_name = ANY(sqlc.arg('job_names')::text[])
  AND status IN ('active', 'waiting')
  AND created_at < sqlc.arg('stale_before')::timestamp;
