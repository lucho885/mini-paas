// Package api contiene la capa HTTP: traduce peticiones a llamadas al
// repositorio y los resultados a JSON con su código de estado.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"mini-paas/internal/app"
	"mini-paas/internal/deploy"

	"github.com/jackc/pgx/v5/pgconn"
)

// Handler agrupa las dependencias que necesitan los endpoints. Es el patrón
// habitual en Go en vez de variables globales: los handlers son métodos y
// acceden a lo que necesitan a través del receptor.
type Handler struct {
	apps     *app.Repository
	deployer *deploy.Deployer
	logger   *slog.Logger
}

func NewHandler(apps *app.Repository, deployer *deploy.Deployer, logger *slog.Logger) *Handler {
	return &Handler{apps: apps, deployer: deployer, logger: logger}
}

// Register añade las rutas al mux. Los patrones "MÉTODO /ruta/{param}" son de
// Go 1.22; {id} se lee después con r.PathValue("id").
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /apps", h.create)
	mux.HandleFunc("GET /apps", h.list)
	mux.HandleFunc("GET /apps/{id}", h.get)
	mux.HandleFunc("DELETE /apps/{id}", h.delete)
	mux.HandleFunc("POST /apps/{id}/deploy", h.startDeploy)
	mux.HandleFunc("GET /apps/{id}/deployments", h.listDeployments)
	mux.HandleFunc("GET /apps/{id}/logs", h.logs)
	mux.HandleFunc("GET /apps/{id}/env", h.getEnv)
	mux.HandleFunc("PUT /apps/{id}/env", h.putEnv)
}

// createRequest es el cuerpo esperado en POST /apps. Es un struct aparte del
// modelo App porque el cliente no debe poder enviar id ni created_at.
type createRequest struct {
	Name    string `json:"name"`
	RepoURL string `json:"repo_url"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	// Limitamos el cuerpo a 1 MB para que nadie nos mande algo gigante.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.RepoURL = strings.TrimSpace(req.RepoURL)
	if req.Name == "" || req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "name y repo_url son obligatorios")
		return
	}

	// Primero lo barato (el nombre), luego lo que hace una llamada de red (el repo).
	if err := deploy.ValidateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Rechazamos aquí un repo inexistente, en vez de descubrirlo al desplegar.
	if err := deploy.CheckRepo(r.Context(), req.RepoURL); err != nil {
		switch {
		case errors.Is(err, deploy.ErrInvalidRepoURL):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, deploy.ErrRepoUnreachable):
			writeError(w, http.StatusBadRequest, "no se pudo acceder al repositorio: comprueba que la URL existe y es pública")
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "el servidor git tardó demasiado en responder")
		default:
			h.internalError(w, err)
		}
		return
	}

	a, err := h.apps.Create(r.Context(), req.Name, req.RepoURL)
	if err != nil {
		// errors.As recorre la cadena de errores (%w) buscando un *PgError.
		// El código 23505 de Postgres es "unique_violation".
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "ya existe una app con ese nombre")
			return
		}
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	apps, err := h.apps.List(r.Context())
	if err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	a, err := h.apps.Get(r.Context(), id)
	if errors.Is(err, app.ErrNotFound) {
		writeError(w, http.StatusNotFound, "app no encontrada")
		return
	}
	if err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	a, err := h.apps.Get(r.Context(), id)
	if errors.Is(err, app.ErrNotFound) {
		writeError(w, http.StatusNotFound, "app no encontrada")
		return
	}
	if err != nil {
		h.internalError(w, err)
		return
	}

	// Primero se apaga lo que está publicado y luego se borra la fila.
	h.deployer.Teardown(r.Context(), a)

	err = h.apps.Delete(r.Context(), id)
	if errors.Is(err, app.ErrNotFound) {
		writeError(w, http.StatusNotFound, "app no encontrada")
		return
	}
	if err != nil {
		h.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseID lee {id} de la ruta y lo convierte a int64. Si falla, ya responde
// 400 y devuelve ok=false para que el handler haga return.
func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return 0, false
	}
	return id, true
}

// internalError registra el detalle en el log y al cliente le da un mensaje
// genérico: no se filtran errores de la base de datos hacia fuera.
func (h *Handler) internalError(w http.ResponseWriter, err error) {
	h.logger.Error("error interno", "error", err)
	writeError(w, http.StatusInternalServerError, "error interno")
}

// writeJSON no existe en la librería estándar (no hay res.json()), así que lo
// escribimos una vez. Primero el header, luego el código, luego el cuerpo.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
