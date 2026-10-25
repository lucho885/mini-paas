package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Estados posibles de un deployment. Go no tiene enums como TypeScript; lo
// habitual es un grupo de constantes. Usarlas en vez de strings sueltos evita
// errores de tipeo ("runing") que el compilador sí detecta.
const (
	StatusPending  = "pending"
	StatusBuilding = "building"
	StatusRunning  = "running"
	StatusFailed   = "failed"
	StatusStopped  = "stopped"
)

// deploymentColumns evita repetir la lista de columnas en cada consulta.
const deploymentColumns = `id, app_id, status, container_id, host_port, created_at`

// scanDeployment lee una fila en un Deployment. Recibe una interfaz mínima en
// vez de *sql.Row o *sql.Rows: ambos tienen el método Scan, así que sirve para
// los dos (las interfaces en Go se cumplen de forma implícita).
//
// &d.ContainerID es un puntero a un puntero (**string): database/sql deja el
// campo en nil cuando la columna es NULL, y lo rellena cuando hay valor.
func scanDeployment(row interface{ Scan(dest ...any) error }) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.AppID, &d.Status, &d.ContainerID, &d.HostPort, &d.CreatedAt)
	return d, err
}

// CreateDeployment registra un nuevo despliegue de la app en estado pending.
func (r *Repository) CreateDeployment(ctx context.Context, appID int64) (Deployment, error) {
	d, err := scanDeployment(r.db.QueryRowContext(ctx,
		`INSERT INTO deployments (app_id, status) VALUES ($1, $2)
		 RETURNING `+deploymentColumns,
		appID, StatusPending))
	if err != nil {
		return Deployment{}, fmt.Errorf("crear deployment: %w", err)
	}
	return d, nil
}

// UpdateStatus cambia el estado de un deployment (building, failed...).
func (r *Repository) UpdateStatus(ctx context.Context, id int64, status string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE deployments SET status = $1 WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("actualizar estado: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("actualizar estado: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkRunning guarda el contenedor y el puerto, y pasa el deployment a running.
func (r *Repository) MarkRunning(ctx context.Context, id int64, containerID string, hostPort int) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE deployments SET status = $1, container_id = $2, host_port = $3
		 WHERE id = $4`,
		StatusRunning, containerID, hostPort, id)
	if err != nil {
		return fmt.Errorf("marcar running: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("marcar running: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// LatestRunning devuelve el deployment activo de la app, o ErrNotFound si no
// hay ninguno. Sirve para parar el contenedor anterior al hacer un redeploy.
func (r *Repository) LatestRunning(ctx context.Context, appID int64) (Deployment, error) {
	d, err := scanDeployment(r.db.QueryRowContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments
		 WHERE app_id = $1 AND status = $2
		 ORDER BY id DESC LIMIT 1`,
		appID, StatusRunning))
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("buscar deployment activo: %w", err)
	}
	return d, nil
}

// RunningDeployment es un deployment activo junto al nombre de su app.
// Deployment va "embebido" (sin nombre de campo): sus campos y métodos se
// pueden usar directamente, r.HostPort en vez de r.Deployment.HostPort. Es la
// forma de Go de reutilizar un struct sin herencia.
type RunningDeployment struct {
	AppName string
	Deployment
}

// ListRunning devuelve los deployments marcados como running, de los más
// antiguos a los más recientes.
func (r *Repository) ListRunning(ctx context.Context) ([]RunningDeployment, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT d.id, d.app_id, d.status, d.container_id, d.host_port, d.created_at, a.name
		 FROM deployments d JOIN apps a ON a.id = d.app_id
		 WHERE d.status = $1 ORDER BY d.id`, StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("listar deployments activos: %w", err)
	}
	defer rows.Close()

	var out []RunningDeployment
	for rows.Next() {
		var rd RunningDeployment
		err := rows.Scan(&rd.ID, &rd.AppID, &rd.Status, &rd.ContainerID, &rd.HostPort, &rd.CreatedAt, &rd.AppName)
		if err != nil {
			return nil, fmt.Errorf("leer deployment activo: %w", err)
		}
		out = append(out, rd)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recorrer deployments activos: %w", err)
	}
	return out, nil
}

// ListDeployments devuelve los deployments de la app, del más reciente al más
// antiguo.
func (r *Repository) ListDeployments(ctx context.Context, appID int64) ([]Deployment, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments
		 WHERE app_id = $1 ORDER BY id DESC`, appID)
	if err != nil {
		return nil, fmt.Errorf("listar deployments: %w", err)
	}
	defer rows.Close()

	deployments := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("leer deployment: %w", err)
		}
		deployments = append(deployments, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recorrer deployments: %w", err)
	}
	return deployments, nil
}
