CREATE TABLE IF NOT EXISTS apps (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    repo_url   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS deployments (
    id           BIGSERIAL PRIMARY KEY,
    app_id       BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending',
    container_id TEXT,
    host_port    INTEGER,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
