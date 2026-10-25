package app

import (
	"context"
	"fmt"
)

// GetEnv devuelve las variables de entorno de la app como un map. Un map[string]string
// es el equivalente a Record<string, string> de TypeScript.
func (r *Repository) GetEnv(ctx context.Context, appID int64) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT key, value FROM app_env WHERE app_id = $1`, appID)
	if err != nil {
		return nil, fmt.Errorf("leer variables: %w", err)
	}
	defer rows.Close()

	// make crea el map vacío; un map nil no se puede escribir (panic).
	env := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("leer variable: %w", err)
		}
		env[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recorrer variables: %w", err)
	}
	return env, nil
}

// SetEnv reemplaza TODAS las variables de la app por las recibidas.
//
// Se hace dentro de una transacción: borrar e insertar son varios pasos y, si
// uno falla a medias, no queremos dejar la app con la mitad de sus variables.
// O se aplican todos los cambios, o ninguno.
func (r *Repository) SetEnv(ctx context.Context, appID int64, env map[string]string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("iniciar transacción: %w", err)
	}
	// Rollback tras un Commit correcto no hace nada, así que este defer es la
	// red de seguridad para cualquier return de error de más abajo.
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM app_env WHERE app_id = $1`, appID); err != nil {
		return fmt.Errorf("borrar variables: %w", err)
	}
	for k, v := range env {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO app_env (app_id, key, value) VALUES ($1, $2, $3)`, appID, k, v)
		if err != nil {
			return fmt.Errorf("guardar variable %s: %w", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("confirmar transacción: %w", err)
	}
	return nil
}
