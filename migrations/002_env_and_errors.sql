-- Variables de entorno por app. Una fila por variable; la clave primaria
-- (app_id, key) impide repetir una clave dentro de la misma app.
CREATE TABLE IF NOT EXISTS app_env (
    app_id BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    key    TEXT NOT NULL,
    value  TEXT NOT NULL,
    PRIMARY KEY (app_id, key)
);

-- Motivo del fallo de un deployment (NULL mientras no haya fallado).
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS error TEXT;
