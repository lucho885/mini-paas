package api

import (
	"errors"
	"net/http"
	"strconv"

	"mini-paas/internal/app"
)

const (
	defaultLogTail = 200
	maxLogTail     = 5000
)

// logs devuelve las últimas líneas del contenedor activo de la app, en texto
// plano. Acepta ?tail=N (1 a 5000, por defecto 200).
func (h *Handler) logs(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	// Los parámetros de la query (?tail=50) llegan siempre como texto: hay que
	// convertirlos y validarlos. Get devuelve "" si el parámetro no viene.
	tail := defaultLogTail
	if raw := r.URL.Query().Get("tail"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxLogTail {
			writeError(w, http.StatusBadRequest, "tail debe ser un número entre 1 y "+strconv.Itoa(maxLogTail))
			return
		}
		tail = n
	}

	if !h.appExists(w, r, id) {
		return
	}

	out, err := h.deployer.Logs(r.Context(), id, tail)
	if errors.Is(err, app.ErrNotFound) {
		writeError(w, http.StatusNotFound, "la app no tiene un contenedor en ejecución")
		return
	}
	if err != nil {
		h.internalError(w, err)
		return
	}

	// Aquí no hay JSON: la respuesta es texto crudo, así que fijamos el
	// Content-Type a mano y escribimos los bytes directamente.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(out))
}
