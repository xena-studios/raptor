-- +goose Up
-- Servers' recent jobs in the mirror (installs, backups, archives, schedule
-- runs), replaced with their server's each time it's fetched: Wings records
-- an event whenever one changes status (job.status), which makes the Panel
-- fetch the server again.
CREATE TABLE m_jobs (
    node_id     uuid        NOT NULL,
    server_id   text        NOT NULL,
    job_id      text        NOT NULL,
    type        text        NOT NULL,
    status      text        NOT NULL,
    attempts    integer     NOT NULL,
    error       text        NOT NULL,
    created_at  timestamptz,
    started_at  timestamptz,
    finished_at timestamptz,
    PRIMARY KEY (node_id, job_id),
    FOREIGN KEY (node_id, server_id) REFERENCES m_servers (node_id, server_id) ON DELETE CASCADE
);
CREATE INDEX m_jobs_server ON m_jobs (node_id, server_id, created_at DESC);

ALTER TABLE m_jobs ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON m_jobs TO raptor_app;
CREATE POLICY m_jobs_read ON m_jobs FOR SELECT TO raptor_app USING (raptor_node_visible(node_id));

-- +goose Down
DROP TABLE m_jobs;
