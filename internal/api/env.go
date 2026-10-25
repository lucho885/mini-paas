package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"mini-paas/internal/app"
)

const (
	maxEnvVars     = 100
	maxEnvKeyLen   = 128
	maxEnvValueLen = 4096
)

// envKeyRe: letras, números y guion bajo, sin empezar por número (lo que
// aceptan los shells y la mayoría de los lenguajes).
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateEnv comprueba las variables antes de guardarlas. Devuelve un error con
// un mensaje pensado para mostrarse al cliente.
func validateEnv(env map[string]string) error {
	if len(env) > maxEnvVars {
		return fmt.Errorf("máximo %d variables", maxEnvVars)
	}
	for k, v := range env {
		if !envKeyRe.MatchString(k) || len(k) > maxEnvKeyLen {
			return fmt.Errorf("clave inválida %q: usa letras, números y _, sin empezar por número (máx. %d caracteres)", k, maxEnvKeyLen)
		}
		if k == "PORT" {
			return errors.New("PORT está reservada: la plataforma la fija en 8080")
		}
		if len(v) > maxEnvValueLen {
			return fmt.Errorf("el valor de %s supera %d caracteres", k, maxEnvValueLen)
		}
		// El archivo de variables es de una línea por variable.
		if strings.ContainsAny(v, "\n\r\x00") {
			return fmt.Errorf("el valor de %s no puede tener saltos de línea", k)
		}
	}
	return nil
}

// getEnv devuelve las variables de entorno de la app.
func (h *Handler) getEnv(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if !h.appExists(w, r, id) {
		return
	}
	env, err := h.apps.GetEnv(r.Context(), id)
	if err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

// putEnv reemplaza TODAS las variables de la app por las del cuerpo
// ({"CLAVE":"valor"}). Se aplican en el próximo deploy, no en el contenedor que
// ya está corriendo.
func (h *Handler) putEnv(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if !h.appExists(w, r, id) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var env map[string]string
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido: se espera un objeto {\"CLAVE\":\"valor\"}")
		return
	}
	if env == nil { // el cuerpo era `null`
		env = map[string]string{}
	}
	if err := validateEnv(env); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.apps.SetEnv(r.Context(), id, env); err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

// appExists comprueba que la app existe; si no, ya responde 404 (o 500) y
// devuelve false para que el handler haga return.
func (h *Handler) appExists(w http.ResponseWriter, r *http.Request, id int64) bool {
	_, err := h.apps.Get(r.Context(), id)
	if errors.Is(err, app.ErrNotFound) {
		writeError(w, http.StatusNotFound, "app no encontrada")
		return false
	}
	if err != nil {
		h.internalError(w, err)
		return false
	}
	return true
}
