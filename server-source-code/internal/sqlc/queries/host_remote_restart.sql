-- name: UpsertHostRemoteRestart :exec
-- Records whether the host's agent allows remote restarts, as it announced
-- it on connecting.
INSERT INTO host_remote_restart (host_id, enabled, reported_at)
VALUES ($1, $2, CURRENT_TIMESTAMP)
ON CONFLICT (host_id) DO UPDATE
SET enabled = EXCLUDED.enabled, reported_at = EXCLUDED.reported_at;

-- name: DeleteHostRemoteRestart :exec
-- Forgets the host's answer: its agent no longer says (older agent).
DELETE FROM host_remote_restart WHERE host_id = $1;

-- name: GetHostRemoteRestart :one
SELECT host_id, enabled, reported_at FROM host_remote_restart WHERE host_id = $1;

-- name: ListHostRemoteRestart :many
SELECT host_id, enabled, reported_at FROM host_remote_restart ORDER BY host_id;
