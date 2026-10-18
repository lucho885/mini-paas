package api

import (
	"errors"
	"net/http"

	"mini-paas/internal/app"
)

// startDeploy crea el registro del deployment y lanza el trabajo en segundo
// plano. Responde 202 Accepted: "recibí la orden, se está procesando". Un 200 o
// 201 mentiría, porque el build aún no ha terminado (puede tardar minutos). El
// cliente consulta el avance con GET /apps/{id}/deployments.
func (h *Handler) startDeploy(w http.ResponseWriter, r *http.Request) {
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

	dep, err := h.apps.CreateDeployment(r.Context(), a.ID)
	if err != nil {
		h.internalError(w, err)
		return
	}

	// Start vuelve al instante; el trabajo sigue en una goroutine.
	h.deployer.Start(a, dep)
	writeJSON(w, http.StatusAccepted, dep)
}

func (h *Handler) listDeployments(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	// Comprobamos que la app existe para distinguir "sin deployments" (200 con
	// lista vacía) de "app inexistente" (404).
	if _, err := h.apps.Get(r.Context(), id); errors.Is(err, app.ErrNotFound) {
		writeError(w, http.StatusNotFound, "app no encontrada")
		return
	} else if err != nil {
		h.internalError(w, err)
		return
	}

	deployments, err := h.apps.ListDeployments(r.Context(), id)
	if err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deployments)
}
