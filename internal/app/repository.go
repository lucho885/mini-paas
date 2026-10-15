package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound es un "error centinela": una variable de paquete que otros
// pueden comparar con errors.Is(err, app.ErrNotFound). En TypeScript harías
// una clase NotFoundError; en Go basta un valor creado con errors.New.
var ErrNotFound = errors.New("app no encontrada")

// Repository agrupa el acceso a la base de datos. Guarda un *sql.DB, que ya es
// un pool de conexiones y es seguro usarlo desde varias goroutines.
type Repository struct {
	db *sql.DB
}

// NewRepository es el "constructor" por convención: Go no tiene clases ni
// constructores, solo funciones normales que devuelven el struct.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Create inserta una app y devuelve la fila creada.
//
// (r *Repository) es el "receptor": convierte la función en método de
// Repository, igual que `this` en una clase. Usamos puntero para no copiar el
// struct en cada llamada.
//
// ctx permite cancelar la consulta si el cliente se desconecta o vence un
// timeout. Por convención siempre es el primer parámetro.
func (r *Repository) Create(ctx context.Context, name, repoURL string) (App, error) {
	var a App
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO apps (name, repo_url) VALUES ($1, $2)
		 RETURNING id, name, repo_url, created_at`,
		name, repoURL,
	).Scan(&a.ID, &a.Name, &a.RepoURL, &a.CreatedAt)
	if err != nil {
		// %w "envuelve" el error original: añade contexto sin perder la causa.
		return App{}, fmt.Errorf("crear app: %w", err)
	}
	return a, nil
}

// List devuelve todas las apps, de la más reciente a la más antigua.
func (r *Repository) List(ctx context.Context) ([]App, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, repo_url, created_at FROM apps ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listar apps: %w", err)
	}
	// defer ejecuta esta línea al salir de la función, pase lo que pase.
	// Es el equivalente a un finally, pero escrito junto a donde abres.
	defer rows.Close()

	apps := []App{}
	for rows.Next() {
		var a App
		if err := rows.Scan(&a.ID, &a.Name, &a.RepoURL, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("leer app: %w", err)
		}
		apps = append(apps, a)
	}
	// rows.Next() devuelve false tanto al terminar como si hubo un error:
	// hay que revisar rows.Err() para distinguirlos.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recorrer apps: %w", err)
	}
	return apps, nil
}

// Get busca una app por id. Devuelve ErrNotFound si no existe.
func (r *Repository) Get(ctx context.Context, id int64) (App, error) {
	var a App
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, repo_url, created_at FROM apps WHERE id = $1`, id,
	).Scan(&a.ID, &a.Name, &a.RepoURL, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, ErrNotFound
	}
	if err != nil {
		return App{}, fmt.Errorf("obtener app: %w", err)
	}
	return a, nil
}

// Delete borra una app (sus deployments caen por ON DELETE CASCADE).
func (r *Repository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM apps WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("borrar app: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("borrar app: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
